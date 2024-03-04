package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"get.pme.sh/pmesh/enats"
	"get.pme.sh/pmesh/rate"
	"get.pme.sh/pmesh/retry"
	"get.pme.sh/pmesh/util"
	"get.pme.sh/pmesh/vhttp"
	"get.pme.sh/pmesh/xlog"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/samber/lo"
)

var schedulerLogger = xlog.NewDomain("sched")

type ScheduledRunner struct {
	Interval util.Duration `yaml:"interval,omitempty"`
	Topic    string        `yaml:"topic,omitempty"`
	Payload  any           `yaml:"payload,omitempty"`
}

func (sch *ScheduledRunner) Run(ctx context.Context, idx int, gw *enats.Gateway, topic, queueName string) {
	subject := ""
	if sch.Topic != "" {
		subject = enats.ToPublisherSubject(sch.Topic)
	} else {
		subject = enats.ToPublisherSubject(topic)
	}

	lock := fmt.Sprintf("%s.%s.%d", subject, queueName, idx)
	log := schedulerLogger.With().Str("id", lock).Logger()
	interval := sch.Interval.Duration()
	if interval <= 0 {
		log.Warn().Dur("interval", interval).Msg("Invalid interval for scheduler")
		return
	}

	var payload []byte
	if sch.Payload != nil {
		if str, ok := sch.Payload.(string); ok {
			payload = []byte(str)
		} else if b, ok := sch.Payload.([]byte); ok {
			payload = b
		} else {
			data, err := json.Marshal(sch.Payload)
			if err != nil {
				log.Err(err).Msg("Failed to marshal payload for scheduler")
			}
			payload = data
		}
	}

	// Establish the first value of the lock
	load := func() (revision uint64, nextRun time.Time, err error) {
		v, e := gw.SchedulerKV.Get(ctx, lock)
		if e == jetstream.ErrKeyNotFound {
			gw.SchedulerKV.Create(ctx, lock, []byte{0})
			v, e = gw.SchedulerKV.Get(ctx, lock)
		}
		if e != nil {
			err = e
			return
		}
		val := v.Value()
		revision = v.Revision()
		if len(val) != 8 {
			nextRun = v.Created()
		} else {
			nextRun = time.UnixMilli(int64(binary.LittleEndian.Uint64(val)))
		}
		return
	}
	xchg := func(nextRun time.Time, revision uint64) error {
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, uint64(nextRun.UnixMilli()))
		_, e := gw.SchedulerKV.Update(ctx, lock, buf, revision)
		return e
	}
	jitter := func(t time.Duration) time.Duration {
		r := rand.NormFloat64()
		r = min(2.0, max(-2.0, r)) * 0.1 // clamp to -0.2..0.2
		return t + time.Duration(float64(t)*r)
	}

	for {
		revision, nextRun, err := load()
		now := time.Now()
		if err != nil {
			log.Warn().Err(err).Msg("Failed to load scheduler state")
			nextRun = now.Add(interval)
		} else if nextRun.Before(now) {
			nextRun = now.Add(interval)
			if err := xchg(nextRun, revision); err == nil {
				//	fmt.Printf("xchg ok %d %s\n", id, nextRun)
				_, err := gw.Jet.Publish(ctx, subject, payload)
				if err != nil {
					log.Err(err).Msg("Failed to publish scheduler message")
					xchg(now, revision+1)
				}
			}
		}

		select {
		case <-time.After(jitter(time.Until(nextRun))):
		case <-ctx.Done():
			return
		}
	}
}

type Runner struct {
	Route    vhttp.HandleMux   `yaml:"route,omitempty"`     // HTTP route for the task
	Content  string            `yaml:"content,omitempty"`   // Content type for the task
	Method   string            `yaml:"method,omitempty"`    // HTTP method for the task
	Schedule []ScheduledRunner `yaml:"schedule,omitempty"`  // Schedule for the task
	Timeout  util.Duration     `yaml:"timeout,omitempty"`   // Timeout for the task
	NakDelay util.Duration     `yaml:"nak_delay,omitempty"` // Delay before NAKing a message
	Rate     rate.Rate         `yaml:"rate,omitempty"`      // Rate limit for the task
	Serial   bool              `yaml:"serial,omitempty"`    // Process messages serially
	Oneshot  bool              `yaml:"oneshot,omitempty"`   // Terminate after the first message
	NoMeta   bool              `yaml:"no_meta,omitempty"`   // Do not include metadata in the request
	Verbose  bool              `yaml:"verbose,omitempty"`   // Log verbose messages
}

func (t *Runner) CreateRequest(ctx context.Context, subject string, data []byte, meta *jetstream.MsgMetadata) (request *http.Request, err error) {
	if t.Content == "http" {
		request, err = http.ReadRequest(bufio.NewReader(bytes.NewReader(data)))
		if err != nil {
			return nil, err
		}
		request = request.WithContext(ctx)
	} else {
		topic, _ := enats.ToTopic(subject)
		if topic == "" {
			return nil, fmt.Errorf("invalid subject: %s", subject)
		}
		fakeUrl := "http://work/" + strings.ReplaceAll(topic, ".", "/")
		method := t.Method

		if method == "" {
			if len(data) == 0 {
				method = "GET"
			} else {
				method = "POST"
			}
		}

		if method == "GET" {
			request, err = http.NewRequestWithContext(ctx, "GET", fakeUrl, nil)
		} else {
			request, err = http.NewRequestWithContext(ctx, method, fakeUrl, bytes.NewReader(data))
		}
		if err != nil {
			return nil, err
		}
		if method != "GET" {
			if t.Content != "" {
				request.Header["Content-Type"] = []string{t.Content}
			} else if t.Content != "unset" {
				request.Header["Content-Type"] = []string{"application/json; charset=utf-8"}
			}
		}
	}
	if meta != nil {
		request.Header["P-CSeq"] = []string{fmt.Sprintf("%d", meta.Sequence.Consumer)}
		request.Header["P-SSeq"] = []string{fmt.Sprintf("%d", meta.Sequence.Stream)}
		request.Header["P-Stream"] = []string{meta.Stream}
		request.Header["P-Consumer"] = []string{meta.Consumer}
		request.Header["P-Domain"] = []string{meta.Domain}
		request.Header["P-Timestamp"] = []string{meta.Timestamp.Format(time.RFC3339Nano)}
		request.Header["P-NumDelivered"] = []string{fmt.Sprintf("%d", meta.NumDelivered)}
		request.Header["P-NumPending"] = []string{fmt.Sprintf("%d", meta.NumPending)}
	}
	return vhttp.StartInternalRequest(request), nil
}
func (t *Runner) ServeMsg(ctx context.Context, subject string, data []byte, meta *jetstream.MsgMetadata) (err error) {
	paniced := true
	defer func() {
		if paniced {
			e := xlog.NewStackErrorf("panic while serving task: %v", recover())
			schedulerLogger.Err(e).Stack().Send()
		}
	}()

	request, err := t.CreateRequest(ctx, subject, data, meta)
	if err != nil {
		paniced = false
		return retry.Disable(fmt.Errorf("failed to create request: %w", err))
	}
	buf := vhttp.NewBufferedResponse(nil)
	t.Route.ServeHTTP(buf, request)

	paniced = false
	if 200 <= buf.Status && buf.Status < 299 {
		return nil
	}
	if 400 <= buf.Status && buf.Status < 499 {
		return retry.Disable(fmt.Errorf("HTTP %d: %s", buf.Status, buf.Body.String()))
	}
	return fmt.Errorf("HTTP %d: %s", buf.Status, buf.Body.String())
}
func (t *Runner) ServeNow(ctx context.Context, msg jetstream.Msg) {
	logger := xlog.Ctx(ctx).With().Str("subject", msg.Subject()).Str("reply", msg.Reply()).Logger()
	if t.Verbose {
		logger.Info().Msg("Task received")
	}
	ctx, cancel := context.WithTimeout(ctx, t.Timeout.Or(5*time.Minute).Duration())
	defer cancel()

	var meta *jetstream.MsgMetadata
	if !t.NoMeta {
		meta, _ = msg.Metadata()
	}
	status := lo.Async(func() error { return t.ServeMsg(ctx, msg.Subject(), msg.Data(), meta) })

	for {
		select {
		case <-ctx.Done():
			logger.Error().Msg("Task timed out")
			if t.Oneshot {
				msg.Term()
			} else {
				msg.Nak()
			}
			return
		case err := <-status:
			if err != nil {
				logger.Err(err).Msg("Failed to execute task")
				if !t.Oneshot && retry.Retryable(err) {
					msg.NakWithDelay(t.NakDelay.Or(1 * time.Minute).Duration())
				} else {
					msg.Term()
				}
			} else {
				if t.Verbose {
					logger.Info().Msg("Task completed")
				}
				msg.Ack()
			}
			return
		case <-time.After(10 * time.Second):
			if err := msg.InProgress(); err != nil {
				logger.Warn().Err(err).Msg("Failed to declare in progress")
			}
		}
	}
}
func (t *Runner) Serve(ctx context.Context, msg jetstream.Msg) {
	if t.Serial {
		t.ServeNow(ctx, msg)
	} else {
		go t.ServeNow(ctx, msg)
	}
}
func (t *Runner) ConsumeContext(ctx context.Context, cns jetstream.Consumer) error {
	if t.Rate.IsZero() {
		consumer, err := cns.Consume(func(msg jetstream.Msg) {
			t.Serve(ctx, msg)
		})
		if err != nil {
			return err
		}
		context.AfterFunc(ctx, consumer.Stop)
	} else {
		go func() {
			left := uint(0)
			next := time.Time{}

			for ctx.Err() == nil {
				// If we're past the previous period, reset the counter
				now := time.Now()
				if next.Before(now) {
					next = now.Add(t.Rate.Period)
					left = t.Rate.Count
				}

				sleep := time.Time{}
				if left <= 0 {
					// If we have no quota left, sleep until the next period
					sleep = next
				} else if msg, err := cns.Next(jetstream.FetchMaxWait(time.Minute)); err != nil {
					// If we have quota left, but next errors, sleep for a bit
					sleep = now.Add(1 * time.Second)
				} else {
					// Run the message handler and decrement the quota
					t.Serve(ctx, msg)
					left--
				}

				if !sleep.IsZero() {
					select {
					case <-ctx.Done():
						return
					case <-time.After(sleep.Sub(now)):
					}
				}
			}
		}()
	}
	return nil
}
func (t *Runner) Listen(ctx context.Context, gw *enats.Gateway, topic string) (context.CancelFunc, error) {
	subjects := enats.ToConsumerSubjects(topic)
	queueName := enats.ToConsumerQueueName("worker-", topic)
	ctx, cancel := context.WithCancel(ctx)
	cns, err := gw.EventStream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:        queueName,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
		AckPolicy:      jetstream.AckExplicitPolicy,
		ReplayPolicy:   jetstream.ReplayInstantPolicy,
		MaxDeliver:     -1,
		MaxAckPending:  -1,
		FilterSubjects: subjects,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	err = t.ConsumeContext(ctx, cns)
	if err != nil {
		cancel()
		return nil, err
	}
	xlog.Info().Str("subject", topic).Str("queue", queueName).Str("stream", gw.EventStream.CachedInfo().Config.Name).Msg("Task listening")
	for i, sch := range t.Schedule {
		go sch.Run(ctx, i, gw, topic, queueName)
	}
	return cancel, nil
}
