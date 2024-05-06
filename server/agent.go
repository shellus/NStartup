package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

// HeartbeatInterval 心跳间隔秒数
const HeartbeatInterval = 60
const IdNone = ""

// NAgent 代理结构
type NAgent struct {
	// 连接，名称ID，最后活跃时间
	conn net.Conn
	// 用来正常退出 Wait
	cancelFunc  context.CancelFunc
	exitContext context.Context
	// exitDone
	exitDone       chan struct{}
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
	exitContext, cancelFunc := context.WithCancel(context.Background())

	return &NAgent{
		conn:        conn,
		cancelFunc:  cancelFunc,
		exitContext: exitContext,
		exitDone:    make(chan struct{}),
		id:          IdNone,
		wolInfos:    nil,
		log:         log.New(os.Stdout, "[NAgent]", log.LstdFlags),
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
func (p *NAgentPool) Dump() string {
	// 返回一行一个，字段包含ID，conn.RemoteAddr
	var dump string
	dump = "total: " + strconv.Itoa(len(p.list)) + "\n"
	for k, v := range p.list {
		dump += k + "," + v.conn.RemoteAddr().String() + "\n"
	}
	return dump
}
func (p *NAgentPool) Find(id string) (*NAgent, bool) {
	agent, ok := p.list[id]
	return &agent, ok
}
func (c *NAgent) Refresh() {
	c.lastActiveTime = time.Now().Unix()
}

func (c *NAgent) ResponseError(err error) {
	buf := make([]byte, 8)
	data := []byte(err.Error())
	binary.LittleEndian.PutUint32(buf[:4], uint32(ResponseError))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(data)))
	_, _ = c.conn.Write(data)
	c.log.Printf("ResponseError: %s", err.Error())
}
func (c *NAgent) ResponseOK() {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint32(buf[:4], uint32(ResponseOK))
	binary.LittleEndian.PutUint32(buf[4:8], 0)
	_, _ = c.conn.Write(buf)
}
func (c *NAgent) Close() {
	// 顺序非常重要
	// 1. 先发信号告诉loop这是正常退出
	// 2. 然后loop自己咔嚓一刀断掉水管
	// 3. 然后等待循环end
	c.cancelFunc()
	<-c.exitDone
}

// Wait 负责解析数据包，不会对NAgent进行任何修改
// 对NAgent的修改应该通过事件总线在NServer层面进行
func (c *NAgent) Wait(bus *Bus) {
	// 监听是否正常退出（cancelFunc）
	isExitSignal := false
	go func() {
		<-c.exitContext.Done()
		_ = c.conn.Close()
		isExitSignal = true
	}()

	c.log.Printf("%s Wait Loop Start", c.conn.RemoteAddr().String())
LOOP:
	for {
		bufHead := make([]byte, 8)
		// 设置超时时间为心跳间隔的两倍
		_ = c.conn.SetReadDeadline(time.Now().Add(HeartbeatInterval * 2 * time.Second))
		// 最小长度8字节，4字节包类型，4字节包长度
		_, err := io.ReadFull(c.conn, bufHead)
		if err != nil {
			// 非正常退出则发送错误事件
			if !isExitSignal {
				c.log.Printf("Wait Read Head Error：%v", err.Error())
				bus.Send(&Event{
					Type:    ConnectionReadError,
					Context: c,
					Data:    err,
				})
			}
			break LOOP
		}
		c.log.Printf("Packet received length: %d", len(bufHead))
		// 包类型
		var packType uint32
		packType = binary.LittleEndian.Uint32(bufHead[:4])
		c.log.Printf("Packet type: %d", packType)

		// 包长度
		var dataLen uint32
		dataLen = binary.LittleEndian.Uint32(bufHead[4:8])
		c.log.Printf("Packet data length: %d", dataLen)
		bufData := make([]byte, dataLen)
		_, err = io.ReadFull(c.conn, bufData)
		if err != nil {
			// 非正常退出则发送错误事件
			if !isExitSignal {
				c.log.Printf("Wait Read Data Error：%v", err.Error())
				bus.Send(&Event{
					Type:    ConnectionReadError,
					Context: c,
					Data:    err,
				})
			}
			break LOOP
		}
		// todo 包类型，是否应该和事件类型分开
		switch EventType(packType) {
		case AgentAuthRequest:
			data := AuthRequest{}
			err = json.Unmarshal(bufData, &data)
			if err != nil {
				c.log.Printf("unmarshal AuthRequest error")
				bus.Send(&Event{
					Type:    ConnectionUnmarshalError,
					Context: c,
					Data:    err,
				})
				break LOOP
			}
			c.log.Printf("AgentAuthRequest packet received. ID: %s", data.ID)
			bus.Send(&Event{
				Type:    AgentAuthRequest,
				Context: c,
				Data:    data,
			})
		case Heartbeat:
			bus.Send(&Event{
				Type:    Heartbeat,
				Context: c,
				Data:    nil,
			})
			c.log.Printf("Heartbeat packet received. ID: %s", c.id)
		default:
			c.log.Printf("Unknown event type")
			bus.Send(&Event{
				Type:    ConnectionUnmarshalError,
				Context: c,
				Data:    err,
			})
			break LOOP
		}
	}
	_ = c.conn.Close()
	c.exitDone <- struct{}{}
	c.log.Printf("%s Wait Loop End", c.conn.RemoteAddr().String())
}
