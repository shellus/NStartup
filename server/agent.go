package server

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
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

	// 用来正常退出 Wait 没有发挥作用，目前使用连接结束来控制退出
	//cancelFunc  context.CancelFunc
	//exitContext context.Context

	// wait循环是否已经退出的chan
	loopClosed chan struct{}
	// 是否已发出退出信号
	isExitSignal   bool
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
	//exitContext, cancelFunc := context.WithCancel(context.Background())

	return &NAgent{
		conn: conn,
		//cancelFunc:   cancelFunc,
		//exitContext:  exitContext,
		loopClosed:   make(chan struct{}),
		isExitSignal: false,
		id:           IdNone,
		wolInfos:     nil,
		log:          log.New(os.Stdout, "[NAgent]", log.LstdFlags),
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
	c.log.Printf("ResponseError: %v", err.Error())
	c.Response(ResponseError, err.Error())
}
func (c *NAgent) ResponseOK(data interface{}) {
	c.log.Printf("ResponseOK")
	c.Response(ResponseOK, data)
}

func (c *NAgent) Response(EventType EventType, data interface{}) {
	// 使用bufio
	w := bufio.NewWriter(c.conn)
	// 写入包类型
	err := binary.Write(w, binary.LittleEndian, uint32(EventType))
	if err != nil {
		c.log.Printf("Response write type Error: %v", err.Error())
	}
	// 序列化data
	var dataBytes []byte
	if data != nil {
		dataBytes, err = json.Marshal(data)
		if err != nil {
			c.log.Printf("Response marshal data Error: %v", err.Error())
		}
	}
	// 写入包长度
	dataLen := uint32(len(dataBytes))
	err = binary.Write(w, binary.LittleEndian, dataLen)
	if err != nil {
		c.log.Printf("Response write dataLen Error: %v", err.Error())
	}
	// 写入包数据
	if dataLen != 0 {
		_, err = w.Write(dataBytes)
		if err != nil {
			c.log.Printf("Response write data Error: %v", err.Error())
		}
	}
	// 刷新缓冲区
	err = w.Flush()
	if err != nil {
		c.log.Printf("Response write Flush Error: %v", err.Error())
	}
	c.log.Printf("Response packet: %d, dataLen: %d sent", EventType, dataLen)
}
func (c *NAgent) connError(bus *Bus, err error) {
	if c.isExitSignal {
		c.log.Printf("exit after conn error ignore: %v", err.Error())
		return
	}

	c.log.Printf("Wait loop read error：%v", err.Error())
	bus.Send(&Event{
		Type:    ConnectionReadError,
		Context: c,
		Data:    err,
	})
}
func (c *NAgent) ReportUnmarshalError(bus *Bus, err error) {
	bus.Send(&Event{
		Type:    ConnectionUnmarshalError,
		Context: c,
		Data:    err,
	})
}
func (c *NAgent) Close() {
	if c.isExitSignal {
		// 禁止重复结束，因为Close函数会阻塞等待退出完成，而只会有一个人可以等待退出完成信号
		panic("duplicate close")
	}
	c.isExitSignal = true // isExitSignal True 后的连接错误都不算错误，是我叫它结束的
	_ = c.conn.Close()    // 结束连接使wait退出循环（read退出阻塞）
	//取消函数没有发挥作用，因为wait循环并没有关联context，以后有其他用途再说
	//c.cancelFunc()
	<-c.loopClosed // 等待循环退出完成，如果在这之前它已经退出了，这里会立即通过。
	c.log.Printf("Connection Close %s", c.conn.RemoteAddr().String())
}

// Wait 负责解析数据包，不会对NAgent进行任何修改
// 对NAgent的修改应该通过事件总线在NServer层面进行
func (c *NAgent) Wait(bus *Bus) {
	// 本来这种循环函数都要能传入context来控制退出
	// 但是这里未实现，而是通过Close来控制退出
	// <-c.exitContext.Done()

	c.log.Printf("%s Wait Loop Start", c.conn.RemoteAddr().String())
	var connErr error
LOOP:
	for {
		bufHead := make([]byte, 8)
		// 设置超时时间为心跳间隔的两倍
		_ = c.conn.SetReadDeadline(time.Now().Add(HeartbeatInterval * 2 * time.Second))
		// 最小长度8字节，4字节包类型，4字节包长度
		_, connErr = io.ReadFull(c.conn, bufHead)
		if connErr != nil {
			break LOOP
		}
		// 包类型
		var packType uint32
		packType = binary.LittleEndian.Uint32(bufHead[:4])

		// 包长度
		var dataLen uint32
		dataLen = binary.LittleEndian.Uint32(bufHead[4:8])
		c.log.Printf("receive packet type: %d, length: %d", packType, dataLen)

		bufData := make([]byte, dataLen)
		_, connErr = io.ReadFull(c.conn, bufData)
		if connErr != nil {
			break LOOP
		}
		// todo 包类型，是否应该和事件类型分开
		switch EventType(packType) {
		case NAgentRegister:
			bus.Send(&Event{
				Type:    NAgentRegister,
				Context: c,
				Data:    nil,
			})
			c.log.Printf("NAgentRegister packet received.")
		case AgentAuthRequest:
			data := AuthRequest{}
			err := json.Unmarshal(bufData, &data)
			if err != nil {
				c.ReportUnmarshalError(bus, err)
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
				Data:    errors.New("unknown event type"),
			})
		}
	}
	c.loopClosed <- struct{}{}

	// 必须是已经有了退出信号，才可以开始调度连接错误
	// 因为连接错误的程序里面肯定有wait closed的功能
	// 如果它们wait的时候我这里还没发出loopClosed，那么就死循环了
	c.connError(bus, connErr)
	c.log.Printf("%s Wait Loop End", c.conn.RemoteAddr().String())
}
