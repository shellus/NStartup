package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/panjf2000/gnet"
	"log"
	"nstartup-server/server/pool"
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
	agentPool pool.KeyPool
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

func (cc *CustomLengthFieldProtocol) Encode(_ gnet.Conn, buf []byte) ([]byte, error) {
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
		err := binary.Read(byteBuffer, binary.LittleEndian, &actionType)
		if err != nil {
			return nil, err
		}
		err = binary.Read(byteBuffer, binary.LittleEndian, &dataLength)
		if err != nil {
			return nil, err
		}
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
	if len(frame) < 8 {
		s.log.Printf("React frame length expect 8, but got %d", len(frame))
		return
	}
	// 包类型
	var packType uint32
	packType = binary.LittleEndian.Uint32(frame[:4])

	// 包长度
	var dataLen uint32
	dataLen = binary.LittleEndian.Uint32(frame[4:8])
	s.log.Printf("receive packet type: %d, length: %d", packType, dataLen)

	// 如果dataLen不为0，则检查frame长度是否足够
	if dataLen != 0 && uint32(len(frame)) < dataLen+8 {
		s.log.Printf("React frame length expect %d, but got %d", dataLen+8, len(frame))
		return
	}
	// 从pool里面用取出agent
	// 或者新建一个
	exists, ok := s.agentPool.FindByConn(c.RemoteAddr().String())
	var agent *NAgent
	var err error
	if !ok {
		agent, err = NewAgent(c)
		if err != nil {
			s.log.Printf("NewAgent Error: %s", err.Error())
			return
		}
		s.agentPool.AddByConn(c.RemoteAddr().String(), agent)
	} else {
		agent = exists.(*NAgent)
	}
	err = agent.OnPacket(s.bus, packType, frame[8:])
	if err != nil {
		s.log.Printf("React OnPacket Error: %s", err.Error())
	}
	return
}

func NewServer(config *NServerConfig) (*NServer, error) {
	if config == nil {
		config = &defaultConfig
	}
	agentPool, err := pool.NewKeyPool()
	if err != nil {
		return nil, err
	}
	bus, err := NewBus()
	if err != nil {
		return nil, err
	}
	s := &NServer{
		config:    *config,
		agentPool: *agentPool,
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
	agent := event.Context.(*NAgent)
	err := event.Data.(error)

	agent.ResponseError(err)
	agent.Close()

	if agent.id != IdNone {
		s.agentPool.RemoveByName(agent.id)
		s.log.Printf("ID %s Connection Close: %s", agent.id, err.Error())
	} else {
		s.log.Printf("No ID Connection Close: %s", err.Error())
	}

	return
}

func (s *NServer) handleNAgentRegister(event *Event) {
	agent := event.Context.(*NAgent)
	id, err := uuid.NewUUID()
	if err != nil {
		agent.ResponseError(err)
		return
	}
	s.log.Printf("NAgentRegister ID: %s", id.String())

	agent.ResponseOK(struct {
		ID string `json:"id"`
	}{
		ID: id.String(),
	})
}

func (s *NServer) handleNAgentAuth(event *Event) {
	agent := event.Context.(*NAgent)
	authRequest := event.Data.(AuthRequest)

	// 检查ID是否为一个UUID
	id, err := uuid.Parse(authRequest.ID)
	if err != nil {
		agent.ResponseError(err)
		agent.Close()
		return
	}

	if old, ok := s.agentPool.FindByName(id.String()); ok {
		oldAgent := old.(*NAgent)
		oldErr := errors.New(fmt.Sprintf("new %s replace Old %s", agent.conn.RemoteAddr().String(), oldAgent.conn.RemoteAddr().String()))
		oldAgent.ResponseError(oldErr)
		oldAgent.Close()
		s.agentPool.RemoveByName(id.String())
	}

	agent.id = id.String()
	agent.wolInfos = authRequest.WOLInfos
	agent.Refresh()

	s.agentPool.AddByConn(agent.conn.RemoteAddr().String(), agent)
	s.agentPool.BindNameToConn(agent.id, agent.conn.RemoteAddr().String())

	s.log.Printf("Agent %s Authenticated in %s", agent.id, agent.conn.RemoteAddr().String())

	agent.ResponseOK(nil)
}
func (s *NServer) handleHeartbeat(event *Event) {
	agent := event.Context.(*NAgent)
	agent.Refresh()
	agent.ResponseOK(nil)
}
