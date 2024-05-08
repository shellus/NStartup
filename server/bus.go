package server

// 一个抽象总线类，包含一个美剧类型和每种类型对应的数据结构
type EventType uint32

const (
	// 100-199 关于数据、业务的处理状态
	ResponseOK    EventType = 100
	ResponseError EventType = 110

	// 500-549 关于传输层的错误
	ConnectionReadError      EventType = 510
	ConnectionUnmarshalError EventType = 512
	//ConnectionReadTimeout    EventType = 514 // 这个超时只能代表绝对的超时，网络层面的，目前看起来没有用到

	// 600-699 服务器内部定义，只用作总线分发，不是外面发来的数据包类型
	//HeartbeatTimeout EventType = 610 // 客户端太久没有发来心跳
	//ResponseTimeout  EventType = 620 // 服务器发出某个请求后，客户端太久没有回应

	// 700-730 关于Agent
	AgentAuthRequest     EventType = 710
	Heartbeat            EventType = 712
	WOLNodeStatusChanged EventType = 720
)

// EventNames 映射事件名称
var EventNames = map[EventType]string{
	ResponseOK:    "响应OK",
	ResponseError: "响应错误",

	ConnectionReadError:      "连接读取错误",
	ConnectionUnmarshalError: "连接反序列化错误",

	AgentAuthRequest:     "代理认证请求",
	Heartbeat:            "心跳",
	WOLNodeStatusChanged: "WOL节点状态改变",
}

type Event struct {
	// 类型
	Type EventType
	// 上下文，一般为事件发起者，例如TCP连接
	Context interface{}
	// 数据
	Data interface{}
}
type Bus struct {
	// 事件处理函数
	handlers map[EventType][]func(*Event)
}

func NewBus() (*Bus, error) {
	bus := Bus{
		handlers: make(map[EventType][]func(*Event)),
	}
	return &bus, nil
}

func (b *Bus) Send(event *Event) {
	if handlers, ok := b.handlers[event.Type]; ok {
		for _, handler := range handlers {
			handler(event)
		}
	}
}

func (b *Bus) RegisterHandler(eventType EventType, handler func(*Event)) {
	b.handlers[eventType] = append(b.handlers[eventType], handler)
}
