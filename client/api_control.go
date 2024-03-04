package client

import (
	"get.pme.sh/pmesh/session"
	"get.pme.sh/pmesh/xpost"

	"github.com/nats-io/nats.go/jetstream"
)

func (c Client) Shutdown() (err error) {
	err = c.Call("/shutdown", nil, nil)
	return
}
func (c Client) Peers() (res []xpost.Peer, err error) {
	err = c.Call("/peers", nil, &res)
	return
}
func (c Client) PeersAlive() (res []xpost.Peer, err error) {
	err = c.Call("/peers/alive", nil, &res)
	return
}
func (c Client) Publish(topic string, p any) (ack jetstream.PubAck, err error) {
	err = c.Call("/publish/"+topic, p, &ack)
	return
}
func (c Client) Reload(invalidate bool) (err error) {
	err = c.Call("/reload", session.ServiceInvalidate{Invalidate: invalidate}, nil)
	return
}
