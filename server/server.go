package server

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

// 客户端结构
type NAgent struct {
	// 连接，名称ID，最后活跃时间
	conn           net.Conn
	id             string
	lastActiveTime int64
	// 承载的 WOL 节点
	wolInfos []WOLInfo
}

type NAgentPool struct {
	list map[string]NAgent
}

// WOL节点信息结构
type WOLInfo struct {
	Name          string `json:"name"`
	MACAddr       string `json:"mac_addr"`
	Port          int    `json:"port"`
	BroadcastAddr string `json:"broadcast_addr"`
	IP            string `json:"ip"`
}

// 客户端认证请求json
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
	// 读取认证信息
	buf := make([]byte, 1024)
	// 设置超时时间
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	n, err := conn.Read(buf)
	if err != nil {
		// 判断err类型是否是超时
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, errors.New("read Timeout")
		}
		return nil, err
	}
	// 解析 AuthRequest
	authRequest := AuthRequest{}
	err = json.Unmarshal(buf[:n], &authRequest)
	if err != nil {
		return nil, errors.New("unmarshal AuthRequest error")
	}
	// 检查ID是否为一个UUID
	id, err := uuid.Parse(authRequest.ID)
	if err != nil {
		return nil, errors.New("ID is not a UUID")
	}
	// 检查WOLInfos是否为空
	if len(authRequest.WOLInfos) == 0 {
		return nil, errors.New("WOLInfos is empty")
	}
	// 检查UUID是否已存在
	if _, ok := p.list[id.String()]; ok {
		return nil, errors.New("ID already exists")
	}
	// 创建一个新的客户端
	agent := NAgent{
		conn:     conn,
		id:       id.String(),
		wolInfos: authRequest.WOLInfos,
	}

	// 添加到pool
	p.list[id.String()] = agent
	// 为客户端启动一个协程
	go agent.wait()

	return &agent, nil
}

func (p *NAgentPool) Remove(id string) {
	delete(p.list, id)
}

func (p *NAgentPool) Find(id string) (*NAgent, bool) {
	agent, ok := p.list[id]
	return &agent, ok
}

func (c *NAgent) wait() {
	// 持续的读取数据
	for {
		buf := make([]byte, 1024)
		// 设置超时时间为1分钟，一般情况下为心跳数据包
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		n, err := c.conn.Read(buf)
		if err != nil {
			// 判断err类型是否是超时
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// todo 退出循环，并向事件总线发出客户端超时事件
				return
			}
			// todo 退出循环，并向事件总线发出客户端读取异常事件
			// todo 事件总线处理程序收到异常事件需要关闭连接并从池移除客户端
			return
		}
		// todo 从头4字节读取到包类型

		// todo 从头4字节读取到包长度

		// todo 如果是心跳包，则刷新最后在线时间

		// todo 如果是WOL节点状态变化事件，则传到事件总线

		// todo 其实所有类型的包都应该传到事件总线进行处理
	}
}

// 服务端结构
type NServerConfig struct {
	Port int
}
type NServer struct {
	config    NServerConfig
	listener  net.Listener
	agentPool NAgentPool
	log       *log.Logger
	// todo 创建事件总线结构
}

// todo 事件总线需要定义每一个事件类型，例如客户端超时事件，客户端读取异常事件，WOL节点状态变化事件等，总之需要定义一个事件类型的枚举

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
	return &NServer{
		config:    *config,
		listener:  listener,
		agentPool: *pool,
		log:       log.New(os.Stdout, "NServer: ", log.LstdFlags),
	}, nil
}

func (s *NServer) Start() error {
	// 开始接收连接
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}
		// 创建一个新的Client
		_, err = s.agentPool.NewAgent(conn)
		if err != nil {
			// log warning
			s.log.Printf("NewAgent error: %v", err)
			// 结束连接， 忽略错误
			_ = conn.Close()
			continue
		}
	}
}
