package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/panjf2000/gnet"
	"log"
	"os"
	"strconv"
)

// 服务端结构
type NServerConfig struct {
	PortTCP int
	PortUDP int
}
type NServer struct {
	*gnet.EventServer
	config    NServerConfig
	agentPool NAgentPool
	log       *log.Logger
	bus       *Bus
}

// 默认配置
var defaultConfig = NServerConfig{
	PortTCP: 8080,
	PortUDP: 8081,
}

type CustomLengthFieldProtocol struct {
	ActionType uint32
	DataLength uint32
	Data       []byte
}

func (cc *CustomLengthFieldProtocol) Encode(c gnet.Conn, buf []byte) ([]byte, error) {
	return buf, nil
}
func (cc *CustomLengthFieldProtocol) Decode(c gnet.Conn) ([]byte, error) {
	fmt.Println("Decode")
	// parse header
	headerLen := 8 // uint32+uint32
	if size, header := c.ReadN(headerLen); size == headerLen {
		byteBuffer := bytes.NewBuffer(header)
		var actionType uint32
		var dataLength uint32
		binary.Read(byteBuffer, binary.LittleEndian, &actionType)
		binary.Read(byteBuffer, binary.LittleEndian, &dataLength)
		// to check the protocol version and actionType,
		// reset buffer if the version or actionType is not correct
		//if pbVersion != DefaultProtocolVersion || isCorrectAction(actionType) == false {
		//	c.ResetBuffer()
		//	log.Println("not normal protocol:", pbVersion, DefaultProtocolVersion, actionType, dataLength)
		//	return nil, errors.New("not normal protocol")
		//}
		// parse payload
		dataLen := int(dataLength) //max int32 can contain 210MB payload
		protocolLen := headerLen + dataLen
		log.Println("protocol:", actionType, dataLength)
		if dataSize, data := c.ReadN(protocolLen); dataSize == protocolLen {
			// 不弹出head部分，因为业务层需要
			c.ShiftN(protocolLen)
			// return the payload of the data
			return data, nil
		}
		return nil, errors.New("not enough payload data")

	}
	return nil, errors.New("not enough header data")
}

func (s *NServer) React(frame []byte, c gnet.Conn) (out []byte, action gnet.Action) {
	// 包类型
	var packType uint32
	packType = binary.LittleEndian.Uint32(frame[:4])

	// 包长度
	var dataLen uint32
	dataLen = binary.LittleEndian.Uint32(frame[4:8])
	s.log.Printf("receive packet type: %d, length: %d", packType, dataLen)

	// 如果 packType 在 NAgentRegister、NAgentAuth 则为匿名请求
	if EventType(packType) == NAgentRegister || EventType(packType) == NAgentAuth {
		OnPacketAnonymous(s.log, c, s.bus, packType, frame[8:])
		return
	}
	agent := c.Context().(*NAgent)
	agent.OnPacket(s.bus, packType, frame[8:])
	return
	//bufData := frame[8:]
}

func NewServer(config *NServerConfig) (*NServer, error) {
	if config == nil {
		config = &defaultConfig
	}
	pool, err := NewAgentPool()
	if err != nil {
		return nil, err
	}
	bus, err := NewBus()
	if err != nil {
		return nil, err
	}
	s := &NServer{
		config:    *config,
		agentPool: *pool,
		log:       log.New(os.Stdout, "[NServer] ", log.LstdFlags),
		bus:       bus,
	}
	bus.RegisterHandler(ConnectionError, s.handleConnectionError)
	bus.RegisterHandler(NAgentRegister, s.handleNAgentRegister)
	bus.RegisterHandler(NAgentAuth, s.handleNAgentAuth)
	bus.RegisterHandler(Heartbeat, s.handleHeartbeat)
	return s, nil
}

func (s *NServer) GetListenAddr() string {
	return ":" + strconv.Itoa(s.config.PortTCP)
}

func (s *NServer) DumpAgentTable() string {
	return s.agentPool.Dump()
}
func (s *NServer) Start(cancelContext context.Context) {
	addr1 := "tcp://:" + strconv.Itoa(s.config.PortTCP)
	addr2 := "udp://:" + strconv.Itoa(s.config.PortTCP)
	// 开始接收连接
	defer func() {
		s.log.Printf("cancelContext Done")
		err := gnet.Stop(context.Background(), addr1)
		if err != nil {
			s.log.Printf("Stop TCP Server Error: %s", err.Error())
		}
		err = gnet.Stop(context.Background(), addr2)
		if err != nil {
			s.log.Printf("Stop UDP Server Error: %s", err.Error())
		}
	}()

	go func() {
		err := gnet.Serve(s, addr1, gnet.WithCodec(&CustomLengthFieldProtocol{}))
		if err != nil {
			s.log.Printf("TCP Server Error: %s", err.Error())
		}
	}()
	go func() {
		err := gnet.Serve(s, addr2)
		if err != nil {
			s.log.Printf("UDP Server Error: %s", err.Error())
		}
	}()

	<-cancelContext.Done()
}

func (s *NServer) handleConnectionError(event *Event) {
	// 1. 认证信息读取超时；没有ID
	// 2. 认证信息读取错误，例如数据结构不对；没有ID
	// 3. 后续错误；有ID；需要移除
	// use of closed network connection 客户端断开连接
	// wsarecv: An existing connection was forcibly closed by the remote host. 客户端断开连接
	agent := event.Context.(*NAgent)
	err := event.Data.(error)

	agent.ResponseError(err)
	agent.Close()

	if agent.id != IdNone {
		s.agentPool.Remove(agent.id)
		s.log.Printf("ID %s Connection Close: %s", agent.id, err.Error())
	} else {
		s.log.Printf("No ID Connection Close: %s", err.Error())
	}

	return
}

func (s *NServer) handleNAgentRegister(event *Event) {
	conn := event.Context.(gnet.Conn)
	id, err := uuid.NewUUID()
	if err != nil {
		conn.AsyncWrite(buildErrorPacket(err))
		return
	}
	conn.AsyncWrite(buildOKPacket(struct {
		ID string `json:"id"`
	}{
		ID: id.String(),
	}))
}

// buildErrorPacket 构建错误包
func buildErrorPacket(err error) []byte {
	buf := bytes.NewBuffer(nil)
	binary.Write(buf, binary.LittleEndian, uint32(ResponseError))
	binary.Write(buf, binary.LittleEndian, uint32(len(err.Error())))
	buf.Write([]byte(err.Error()))
	return buf.Bytes()
}
func buildOKPacket(data interface{}) []byte {
	buf := bytes.NewBuffer(nil)
	binary.Write(buf, binary.LittleEndian, uint32(ResponseOK))
	if data != nil {
		dataBytes, err := json.Marshal(data)
		if err != nil {
			return buildErrorPacket(err)
		}
		binary.Write(buf, binary.LittleEndian, uint32(len(dataBytes)))
		buf.Write(dataBytes)
	} else {
		binary.Write(buf, binary.LittleEndian, uint32(0))
	}
	return buf.Bytes()
}
func (s *NServer) handleNAgentAuth(event *Event) {
	conn := event.Context.(gnet.Conn)
	authRequest := event.Data.(AuthRequest)

	// 检查ID是否为一个UUID
	id, err := uuid.Parse(authRequest.ID)
	if err != nil {
		conn.AsyncWrite(buildErrorPacket(err))
		conn.Close()
		return
	}
	// 允许空WOL上线，因为这个要改成可以服务端下发配置
	// 检查WOLInfos是否为空
	//if len(authRequest.WOLInfos) == 0 {
	//	conn.ResponseError(errors.New("WOLInfos is empty"))
	//	conn.Close()
	//	return
	//}
	// 检查UUID是否已存在
	// 这里，如果是客户端异常断开，可能有5-10秒左右才会在服务端的recv函数中出错
	// 那么，如果在这个5-10秒内客户端再次连接上来，会导致ID重复错误，其实不合理
	// 新连接顶替旧的连接，向旧的连接发送异地登陆错误并Close连接
	if old, ok := s.agentPool.Find(id.String()); ok {
		old.ResponseError(errors.New("new Connection Replace Old Connection"))
		old.Close()
		s.agentPool.Remove(old.id)
	}

	agent, err := s.agentPool.NewAgent(conn)
	agent.id = id.String()
	agent.wolInfos = authRequest.WOLInfos
	agent.Refresh()
	s.agentPool.Add(agent)
	conn.SetContext(agent)
	s.log.Printf("Agent %s Authenticated in %s", agent.id, agent.conn.RemoteAddr().String())

	conn.AsyncWrite(buildOKPacket(nil))
}
func (s *NServer) handleHeartbeat(event *Event) {
	agent := event.Context.(*NAgent)
	agent.Refresh()
	agent.ResponseOK(nil)
}
