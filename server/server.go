package server

import (
	"context"
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
	bus.RegisterHandler(ConnectionUnmarshalError, s.handleContentError)
	bus.RegisterHandler(AgentAuthRequest, s.handleAgentAuthRequest)
	bus.RegisterHandler(Heartbeat, s.handleHeartbeat)
	return s, nil
}

func (s *NServer) GetListenAddr() string {
	return s.listener.Addr().String()
}

func (s *NServer) DumpAgentTable() string {
	return s.agentPool.Dump()
}
func (s *NServer) Start(cancelContext context.Context) error {
	// 开始接收连接
	go func() {
		<-cancelContext.Done()
		s.log.Printf("cancelContext Done")
		_ = s.listener.Close()
	}()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// 判断如果是主动关闭，返回nil
			if err, ok := err.(*net.OpError); ok && err.Err.Error() == "use of closed network connection" {
				return nil
			}
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
	// use of closed network connection 客户端断开连接
	// wsarecv: An existing connection was forcibly closed by the remote host. 客户端断开连接
	agent := event.Context.(*NAgent)
	err := event.Data.(error)
	if agent.id == IdNone {
		s.log.Printf("No ID Connection Close: %s", err.Error())
	} else {
		s.log.Printf("ID %s Connection Close: %s", agent.id, err.Error())
		s.agentPool.Remove(agent.id)
	}
	return
}

func (s *NServer) handleAgentAuthRequest(event *Event) {
	agent := event.Context.(*NAgent)
	authRequest := event.Data.(AuthRequest)
	// 检查ID是否为一个UUID
	id, err := uuid.Parse(authRequest.ID)
	if err != nil {
		agent.ResponseError(errors.New("ID is not a UUID"))
		agent.Close()
		return
	}
	// 检查WOLInfos是否为空
	if len(authRequest.WOLInfos) == 0 {
		agent.ResponseError(errors.New("WOLInfos is empty"))
		agent.Close()
		return
	}
	// 检查UUID是否已存在
	// 这里，如果是客户端异常断开，可能有5-10秒左右才会在服务端的recv函数中出错
	// 那么，如果在这个5-10秒内客户端再次连接上来，会导致ID重复错误，其实不合理
	// 新连接顶替旧的连接，向旧的连接发送异地登陆错误并Close连接
	if old, ok := s.agentPool.Find(id.String()); ok {
		old.ResponseError(errors.New("new Connection Replace Old Connection"))
		old.Close()
	}

	agent.id = id.String()
	agent.wolInfos = authRequest.WOLInfos
	agent.Refresh()
	s.agentPool.Add(agent)
	s.log.Printf("Agent %s Authenticated", agent.id)

	agent.ResponseOK()
}
func (s *NServer) handleHeartbeat(event *Event) {
	agent := event.Context.(*NAgent)
	agent.Refresh()
	agent.ResponseOK()
}
