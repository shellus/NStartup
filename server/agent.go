package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/panjf2000/gnet"
	"log"
	"os"
	"time"
)

// HeartbeatInterval 心跳间隔秒数
const HeartbeatInterval = 60
const IdNone = ""

// NAgent 代理结构
type NAgent struct {
	// 连接，名称ID，最后活跃时间
	conn  gnet.Conn
	isUDP bool
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

func NewAgent(conn gnet.Conn) (*NAgent, error) {
	return &NAgent{
		conn:         conn,
		isUDP:        conn.LocalAddr().Network() == "udp",
		isExitSignal: false,
		id:           IdNone,
		wolInfos:     nil,
		log:          log.New(os.Stdout, "[NAgent]", log.LstdFlags),
	}, nil
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
	w := bytes.NewBuffer(nil)
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
	// 根据连接是UDP还是TCP选择使用SendTo或者AsyncWrite
	if c.isUDP {
		err = c.conn.SendTo(w.Bytes())
	} else {
		err = c.conn.AsyncWrite(w.Bytes())
	}
	if err != nil {
		c.log.Printf("Response write Error: %v", err.Error())
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
		Type:    ConnectionError,
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
	c.log.Printf("Connection Close %s", c.conn.RemoteAddr().String())
}

func (c *NAgent) OnPacket(bus *Bus, packType uint32, bufData []byte) error {
	switch EventType(packType) {
	case NAgentRegister:
		bus.Send(&Event{
			Type:    NAgentRegister,
			Context: c,
			Data:    nil,
		})
		c.log.Printf("NAgentRegister packet received. Conn: %s", c.conn.RemoteAddr().String())
	case NAgentAuth:
		data := AuthRequest{}
		err := json.Unmarshal(bufData, &data)
		if err != nil {
			return err
		}
		bus.Send(&Event{
			Type:    NAgentAuth,
			Context: c,
			Data:    data,
		})
		c.log.Printf("NAgentAuth packet received. ID: %s", c.id)
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
			Type:    ConnectionError,
			Context: c,
			Data:    errors.New("unknown event type"),
		})
	}
	return nil
}
func OnPacketAnonymous(logServer *log.Logger, c gnet.Conn, bus *Bus, packType uint32, bufData []byte) error {
	// 这个IF里面的上下文是连接，而不是Agent
	if EventType(packType) == NAgentRegister {
		bus.Send(&Event{
			Type:    NAgentRegister,
			Context: c,
			Data:    nil,
		})
		return nil
	} else if EventType(packType) == NAgentAuth {
		data := AuthRequest{}
		err := json.Unmarshal(bufData, &data)
		if err != nil {
			return err
		}
		bus.Send(&Event{
			Type:    NAgentAuth,
			Context: c,
			Data:    data,
		})
		return nil
	}

	return nil
}
