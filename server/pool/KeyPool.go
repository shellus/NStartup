package pool

// 设置一个容纳interface的结构，允许使用两类str key来索引，一个是Remote Addr String 作为 唯一key， 一个是客户端序列号作为name
// 认证之前，只有conn，认证之后，才有name

type KeyPool struct {
	connList   map[string]interface{}
	nameToConn map[string]string
}

func NewKeyPool() (*KeyPool, error) {
	return &KeyPool{
		connList:   make(map[string]interface{}),
		nameToConn: make(map[string]string),
	}, nil
}

func (p *KeyPool) AddByConn(conn string, agent interface{}) {
	p.connList[conn] = agent
}

func (p *KeyPool) RemoveByConn(conn string) {
	delete(p.connList, conn)
	// 同时删除nameToId中的对应项
	for k, v := range p.nameToConn {
		if v == conn {
			delete(p.nameToConn, k)
		}
	}
}
func (p *KeyPool) RemoveByName(name string) {
	conn, ok := p.nameToConn[name]
	if !ok {
		return
	}
	delete(p.nameToConn, name)
	delete(p.connList, conn)
}

func (p *KeyPool) FindByConn(conn string) (interface{}, bool) {
	agent, ok := p.connList[conn]
	return agent, ok
}

func (p *KeyPool) BindNameToConn(name string, conn string) {
	p.nameToConn[name] = conn
}

func (p *KeyPool) FindByName(name string) (interface{}, bool) {
	conn, ok := p.nameToConn[name]
	if !ok {
		return nil, false
	}
	return p.FindByConn(conn)
}

func (p *KeyPool) Dump() string {
	var result string
	result = "conn List:\n"
	for k, v := range p.connList {
		result += k + " : " + v.(string) + "\n"
	}
	result += "Name to conn:\n"
	for k, v := range p.nameToConn {
		result += k + " : " + v + "\n"
	}
	return result
}
