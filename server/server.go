package server

import (
	"errors"
	"github.com/google/uuid"
	"log"
	"net"
	"os"
	"strconv"
)

// 服务端结构
type NServerConfig struct {
	Port int
}
type NServer struct {
	config    NServerConfig
	listener  net.Listener
	agentPool NAgentPool
	log       *log.Logger
	bus       *Bus
}

// 默认配置
var defaultConfig = NServerConfig{
	Port: 8080,
}

func NewServer(config *NServerConfig) (*NServer, error) {
	if config == nil {
		config = &defaultConfig
	}
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(config.Port))
	if err != nil {
		return nil, err
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
		listener:  listener,
		agentPool: *pool,
		log:       log.New(os.Stdout, "[NServer] ", log.LstdFlags),
		bus:       bus,
	}
	bus.RegisterHandler(ConnectionReadError, s.handleContentError)
	bus.RegisterHandler(ConnectionReadTimeout, s.handleContentError)
	bus.RegisterHandler(AgentAuthRequest, s.handleAgentAuthRequest)
	return s, nil
}

func (s *NServer) Start() error {
	// 开始接收连接
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}
		// 创建一个新的Client
		agent, err := s.agentPool.NewAgent(conn)
		if err != nil {
			// log warning
			s.log.Printf("NewAgent error: %v", err)
			// 结束连接， 忽略错误
			_ = conn.Close()
			continue
		}
		// 为客户端启动一个协程
		go agent.Wait(s.bus)
	}
}

func (s *NServer) handleContentError(event *Event) {
	// 1. 认证信息读取超时；没有ID
	// 2. 认证信息读取错误，例如数据结构不对；没有ID
	// 3. 后续错误；有ID；需要移除
	agent := event.Context.(*NAgent)
	if agent.id == IdNone {
		agent.Close()
		return
	}
}
func (s *NServer) handleAgentAuthRequest(event *Event) {
	agent := event.Context.(*NAgent)
	authRequest := event.Data.(AuthRequest)
	// 检查ID是否为一个UUID
	id, err := uuid.Parse(authRequest.ID)
	if err != nil {
		agent.SendError(errors.New("ID is not a UUID"))
		agent.Close()
		return
	}
	// 检查WOLInfos是否为空
	if len(authRequest.WOLInfos) == 0 {
		agent.SendError(errors.New("WOLInfos is empty"))
		agent.Close()
		return
	}
	// 检查UUID是否已存在
	if s.agentPool.Exists(id.String()) {
		agent.SendError(errors.New("ID already exists"))
		agent.Close()
		return
	}

	agent.id = id.String()
	agent.wolInfos = authRequest.WOLInfos
	agent.Refresh()
	s.agentPool.Add(agent)
	s.log.Printf("Agent %s Authenticated", agent.id)
}
