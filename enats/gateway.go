package enats

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"get.pme.sh/pmesh/autonats"
	"get.pme.sh/pmesh/config"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/samber/lo"
)

type Client struct {
	*nats.Conn
	Jet jetstream.JetStream
}

type Gateway struct {
	Client
	Server *autonats.Server
	url    string

	// Internal resources
	PeerKV, SchedulerKV jetstream.KeyValue
	// Global resources for the user
	DefaultKV, DailyKV, WeeklyKV, MonthlyKV jetstream.KeyValue

	EventStream jetstream.Stream
}

func New() (r *Gateway) {
	r = &Gateway{}

	if config.Get().Role == config.RoleClient {
		r.url = config.Get().Remote
	} else {
		r.Server = lo.Must(autonats.StartServer(autonats.Options{
			ServerName:  config.Get().Host,
			ClusterName: config.Get().Cluster,
			Secret:      config.Get().Secret,
			Addr:        *config.BindAddr,
			Port:        *config.InternalPort,
			LocalAddr:   *config.LocalBindAddr,
			StoreDir:    config.NatsDir(config.Get().Host),
			Advertise:   config.Get().Advertised,
			Topology:    config.Get().Topology,
		}))
		r.url = r.Server.ClientURL()
	}

	os.Setenv("PM3_NATS", r.url)
	return
}
func (r *Gateway) Open(ctx context.Context) (err error) {
	if r.Server == nil {
		if strings.HasPrefix(r.url, "nats://") {
			r.Client.Conn, err = nats.Connect(r.url)
		} else {
			if strings.IndexByte(r.url, ':') == -1 && *config.InternalPort != 8443 {
				r.url += ":" + strconv.Itoa(*config.InternalPort)
			}
			r.Client.Conn, err = autonats.Connect(
				[]string{r.url},
				config.Get().Secret,
			)
		}
	} else {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.Server.Ready():
		}
		r.Client.Conn, err = r.Server.Connect()
	}
	if err != nil {
		return
	}
	r.Client.Jet, err = jetstream.New(r.Client.Conn)
	if err != nil {
		r.Client.Close()
		return
	}

	// Create the resources.
	{
		r.PeerKV, err = r.KVStore(ctx, jetstream.KeyValueConfig{
			Bucket:       "peers",
			Description:  "PMesh peer discovery",
			MaxValueSize: -1,
			Storage:      jetstream.MemoryStorage,
		})
		if err != nil {
			return
		}
		r.SchedulerKV, err = r.KVStore(ctx, jetstream.KeyValueConfig{
			Bucket:       "sched",
			Description:  "PMesh scheduler locks",
			MaxValueSize: -1,
			Storage:      jetstream.FileStorage,
		})
		if err != nil {
			return
		}
		makeKV := func(kv *jetstream.KeyValue, name string, ttl time.Duration) {
			*kv, err = r.KVStore(ctx, jetstream.KeyValueConfig{
				Bucket:       name,
				Description:  "PMesh",
				MaxValueSize: -1,
				Storage:      jetstream.FileStorage,
				TTL:          ttl,
			})
		}
		if makeKV(&r.DefaultKV, "global", 0); err != nil {
			return
		}
		if makeKV(&r.DailyKV, "daily", 24*time.Hour); err != nil {
			return
		}
		if makeKV(&r.WeeklyKV, "weekly", 7*24*time.Hour); err != nil {
			return
		}
		if makeKV(&r.MonthlyKV, "monthly", 30*24*time.Hour); err != nil {
			return
		}

		r.EventStream, err = r.Stream(ctx, jetstream.StreamConfig{
			Name:         "ev",
			Description:  "PMesh Global event stream",
			Storage:      jetstream.FileStorage,
			Retention:    jetstream.LimitsPolicy,
			Discard:      jetstream.DiscardOld,
			MaxMsgs:      -1,
			MaxMsgSize:   -1,
			MaxAge:       0,
			MaxConsumers: 0,
			MaxBytes:     -1,
			Duplicates:   0,
			AllowDirect:  true,
			Subjects:     []string{"ev.>"},
		})
		if err != nil {
			return
		}
	}
	return nil

}
func (r *Gateway) Close(ctx context.Context) (err error) {
	if cli := r.Client; cli.Conn != nil {
		r.Client.Conn = nil
		select {
		case <-ctx.Done():
		case err = <-lo.Async(cli.Drain):
		}
		cli.Close()
	}
	if r.Server != nil {
		err = errors.Join(err, r.Server.Shutdown(ctx))
	}
	return
}

// Gets the KV store for the default kv usage given the key.
func (r *Gateway) DefaultKVStore(key string) (kv jetstream.KeyValue) {
	if strings.HasPrefix(key, "d.") {
		kv = r.DailyKV
	} else if strings.HasPrefix(key, "w.") {
		kv = r.WeeklyKV
	} else if strings.HasPrefix(key, "m.") {
		kv = r.MonthlyKV
	} else {
		kv = r.DefaultKV
	}
	return
}

// Wrappers
func (r *Client) KVStore(ctx context.Context, cfg jetstream.KeyValueConfig) (kv jetstream.KeyValue, err error) {
	kv, err = r.Jet.CreateOrUpdateKeyValue(ctx, cfg)
	return
}
func (r *Client) Stream(ctx context.Context, cfg jetstream.StreamConfig) (str jetstream.Stream, err error) {
	str, err = r.Jet.CreateOrUpdateStream(ctx, cfg)
	return
}
func (r *Client) Consumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (c jetstream.Consumer, err error) {
	c, err = r.Jet.CreateOrUpdateConsumer(ctx, stream, cfg)
	return
}
