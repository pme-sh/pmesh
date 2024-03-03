package autonats

import (
	"context"
	"encoding/json"
	"time"

	natssrv "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

type Topology map[string][]string // Cluster -> Hostname

func DiscoverTopologyConn(ctx context.Context, conn *nats.Conn) (servers Topology, err error) {
	sub, err := conn.SubscribeSync(conn.NewRespInbox())
	defer sub.Unsubscribe()

	msg := nats.NewMsg("$SYS.REQ.SERVER.PING")
	msg.Data = []byte("{}")
	msg.Reply = sub.Subject
	err = conn.PublishMsg(msg)
	if err != nil {
		return nil, err
	}

	var serversList []*natssrv.ServerInfo
	for {
		var msg *nats.Msg
		msg, err = sub.NextMsgWithContext(ctx)
		if err != nil {
			break
		}
		type response struct {
			Server *natssrv.ServerInfo `json:"server"`
		}
		var resp response
		err = json.Unmarshal(msg.Data, &resp)
		if err != nil {
			return nil, err
		}
		serversList = append(serversList, resp.Server)
	}

	if len(serversList) == 0 {
		return
	}

	servers = make(map[string][]string, len(serversList))
	for _, server := range serversList {
		servers[server.Cluster] = append(servers[server.Cluster], server.Host)
	}
	err = nil
	return
}

func ExpandSeedTopology(hosts []string, secret string, timeout time.Duration) (servers Topology, err error) {
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	nc, err := Connect(hosts, secret, WithSystemAccount())
	if err != nil {
		return
	}
	defer nc.Close()
	return DiscoverTopologyConn(ctx, nc)
}
