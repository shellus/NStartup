package server

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"net"
	"os"
	"time"
)

// HeartbeatInterval 心跳间隔秒数
const HeartbeatInterval = 60
const IdNone = ""

// NAgent 代理结构
type NAgent struct {
	// 连接，名称ID，最后活跃时间
	conn           net.Conn
	id             string
	lastActiveTime int64
	// 承载的 WOL 节点
	wolInfos []WOLInfo
	// 是否已经认证上线
	//authed  bool // 不需要，看lastActiveTime是否为0即可
	log *log.Logger
}

type NAgentPool struct {
	list map[string]NAgent
}

// WOLInfo WOL节点信息结构
type WOLInfo struct {
	Name          string `json:"name"`
	MACAddr       string `json:"mac_addr"`
	Port          int    `json:"port"`
	BroadcastAddr string `json:"broadcast_addr"`
	IP            string `json:"ip"`
}

// AuthRequest 客户端认证请求json
type AuthRequest struct {
	// 客户端ID
	ID string `json:"id"`
	// WOLInfo 数组
	WOLInfos []WOLInfo `json:"wol_infos"`
}

func NewAgentPool() (*NAgentPool, error) {
	return &NAgentPool{
		list: make(map[string]NAgent),
	}, nil
}

func (p *NAgentPool) NewAgent(conn net.Conn) (*NAgent, error) {
	return &NAgent{
		conn:     conn,
		id:       IdNone,
		wolInfos: nil,
		log:      log.New(os.Stdout, "[NAgent]", log.LstdFlags),
	}, nil
}

func (p *NAgentPool) Remove(id string) {
	delete(p.list, id)
}

func (p *NAgentPool) Exists(id string) bool {
	_, ok := p.list[id]
	return ok
}
func (p *NAgentPool) Add(agent *NAgent) {
	p.list[agent.id] = *agent
}
func (p *NAgentPool) Find(id string) (*NAgent, bool) {
	agent, ok := p.list[id]
	return &agent, ok
}
func (c *NAgent) Refresh() {
	c.lastActiveTime = time.Now().Unix()
}
func (c *NAgent) Close() {
	// 这里CLose了，Wait里面的循环会退出
	c.conn.Close()
}
func (c *NAgent) SendError(err error) {
	// todo 这里是否应该是一个错误结构体
	c.conn.Write([]byte(err.Error()))
}

// Wait 负责解析数据包，不会对NAgent进行任何修改
// 对NAgent的修改应该通过事件总线在NServer层面进行

func (c *NAgent) Wait(bus *Bus) {
	for {
		buf := make([]byte, 1024)
		// 设置超时时间为心跳间隔的两倍
		c.conn.SetReadDeadline(time.Now().Add(HeartbeatInterval * 2 * time.Second))

		_, err := c.conn.Read(buf)
		// 错误的两种情况
		// ConnectionReadTimeout: 一般为客户端网络异常
		// ConnectionReadError: 一般为客户端进程退出
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				c.log.Printf("ConnectionReadTimeout")
				bus.Send(&Event{
					Type:    ConnectionReadTimeout,
					Context: c,
					Data:    nil,
				})
			} else {
				c.log.Printf("ConnectionReadError")
				bus.Send(&Event{
					Type:    ConnectionReadError,
					Context: c,
					Data:    nil,
				})
			}
			return
		}
		c.log.Printf("Packet received length: %d", len(buf))
		// 包类型
		var packType uint32
		packType = binary.LittleEndian.Uint32(buf[:4])

		// 包长度
		var dataLen uint32
		dataLen = binary.LittleEndian.Uint32(buf[4:8])

		// todo 包类型，是否应该和事件类型分开
		switch EventType(packType) {
		case AgentAuthRequest:
			data := AuthRequest{}
			err = json.Unmarshal(buf[8:dataLen], &data)
			if err != nil {
				c.log.Printf("unmarshal AuthRequest error")
				bus.Send(&Event{
					Type:    ConnectionReadError,
					Context: c,
					Data:    nil,
				})
				return
			}
			c.log.Printf("AgentAuthRequest packet received. ID: %s", data.ID)
			bus.Send(&Event{
				Type:    AgentAuthRequest,
				Context: c,
				Data:    data,
			})
		default:
			c.log.Printf("Unknown event type")
		}

		// todo 如果是心跳包，则刷新最后在线时间

		// todo 如果是WOL节点状态变化事件，则传到事件总线

		// todo 所有类型的包都应该传到事件总线进行处理
	}
}
