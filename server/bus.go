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
	//事件chan
	eventChan chan *Event
	// 事件处理函数
	handlers map[EventType][]func(*Event)
}

func NewBus() (*Bus, error) {
	bus := Bus{
		eventChan: make(chan *Event),
		handlers:  make(map[EventType][]func(*Event)),
	}
	go bus.handle()
	return &bus, nil
}

func (b *Bus) handle() {
	for {
		event := <-b.eventChan
		for _, handler := range b.handlers[event.Type] {
			// 这个处理函数阻塞，导致chan里面的数据没人取出，导致下一个send卡住
			// 具体分析过程见：《chan阻塞问题.xmind》
			// 简略内容：
			// 1. 在handler后续的调用栈中，我们无法保证到哪里去，没办法保证后续的调用栈不会调用send
			// 2. 甚至，没法保证后续只调用1次
			// 3. 多线程不在此考虑范围，那只需要在handler处做goroutine即可，就算不做，也只是多线程的send变单线程的handler而已，并不会死锁
			// handler(event)

			// 改为goroutine
			// 理由是，假设没有bus总线，是否是来了多少连接、多少请求，就会发起多少调用，bus并没有创造无限goroutine, 只是继承调用处的意愿

			// 还是改为同步吧，bus事件别环回了，做成单向的，智能内部向外部抛出，外部不会同级循环抛出，更不会向深层抛出
			handler(event)
		}
	}
}

func (b *Bus) Send(event *Event) {
	b.eventChan <- event
}

func (b *Bus) RegisterHandler(eventType EventType, handler func(*Event)) {
	b.handlers[eventType] = append(b.handlers[eventType], handler)
}
