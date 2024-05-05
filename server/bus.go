package server

// 一个抽象总线类，包含一个美剧类型和每种类型对应的数据结构
type EventType uint32

const (
	AgentAuthRequest      EventType = iota
	ConnectionReadError   EventType = iota
	ConnectionReadTimeout EventType = iota
	WOLNodeStatusChanged  EventType = iota
)

// 映射事件名称
var EventName = map[EventType]string{
	AgentAuthRequest:      "代理认证请求",
	ConnectionReadError:   "连接读取错误",
	ConnectionReadTimeout: "连接读取超时",
	WOLNodeStatusChanged:  "WOL节点状态改变",
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
	//事件chan
	eventChan chan *Event
	// 事件处理函数
	handlers map[EventType][]func(*Event)
}

// 创建一个新的总线
func NewBus() (*Bus, error) {
	return &Bus{
		eventChan: make(chan *Event),
		handlers:  make(map[EventType][]func(*Event)),
	}, nil
}

// 向总线发送一个事件
func (b *Bus) Send(event *Event) {
	b.eventChan <- event
}

// 注册一个事件处理函数
func (b *Bus) RegisterHandler(eventType EventType, handler func(*Event)) {
	b.handlers[eventType] = append(b.handlers[eventType], handler)
}
