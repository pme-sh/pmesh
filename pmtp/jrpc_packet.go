package pmtp

import (
	"bytes"
	"encoding/json"
	"sync"
)

var packetPool = sync.Pool{
	New: func() any {
		return &jrpcPacket{}
	},
}

type jrpcSeq uint32

const (
	FlagReply jrpcSeq = 1 << 31
	FlagMask  jrpcSeq = (1 << 31) - 1
)

type jrpcPacket struct {
	ID     string          // req: method, reply: error
	Body   json.RawMessage // req: params, reply: result
	Signal chan jrpcSeq
}

func (p *jrpcPacket) Encode(id string, body any) error {
	p.ID = id
	p.Body = p.Body[:0]
	if body != nil {
		if raw, ok := body.(json.RawMessage); ok {
			p.Body = raw
			return nil
		} else {
			buf := bytes.NewBuffer(p.Body[:0])
			enc := json.NewEncoder(buf)
			enc.SetEscapeHTML(false)
			err := enc.Encode(body)
			p.Body = buf.Bytes()
			return err
		}
	}
	return nil
}

func (p *jrpcPacket) Release() {
	p.ID = ""
	p.Body = p.Body[:0]
	packetPool.Put(p)
}
func (p *jrpcPacket) Steal() (body json.RawMessage) {
	body = p.Body
	p.Body = nil
	return
}

func newJrpcPacket() *jrpcPacket {
	return packetPool.Get().(*jrpcPacket)
}
