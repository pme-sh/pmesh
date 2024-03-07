package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"get.pme.sh/pmesh/enats"
	"get.pme.sh/pmesh/rate"
	"get.pme.sh/pmesh/retry"
	"get.pme.sh/pmesh/util"
	"get.pme.sh/pmesh/vhttp"
	"get.pme.sh/pmesh/xlog"

	"github.com/nats-io/nats.go"
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
		subject = enats.ToSubject(sch.Topic)
	} else {
		subject = enats.ToSubject(topic)
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
				err := gw.Publish(subject, payload)
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
	Route        vhttp.HandleMux   `yaml:"route,omitempty"`          // HTTP route for the task
	Schedule     []ScheduledRunner `yaml:"schedule,omitempty"`       // Schedule for the task
	Rate         rate.Rate         `yaml:"rate,omitempty"`           // Rate limit for the task
	NoDeadLetter bool              `yaml:"no_dead_letter,omitempty"` // Do not send to dead letter
	retry.Policy `yaml:",inline"`
}

func (t *Runner) ServeMsg(ctx context.Context, subject string, data []byte, meta *jetstream.MsgMetadata, headers nats.Header) (res []byte, err error) {
	paniced := true
	defer func() {
		if paniced {
			e := xlog.NewStackErrorf("panic while serving task: %v", recover())
			schedulerLogger.Error().Stack().Err(e).Send()
		}
	}()

	// Create a request representing the message
	topic := enats.ToTopic(subject)
	url := "http://worker/" + strings.ReplaceAll(topic, ".", "/")
	request, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		paniced = false
		return nil, retry.Disable(fmt.Errorf("failed to create request: %w", err))
	}

	// Add metadata to the request
	if meta != nil {
		request.Header["C-ID"] = []string{meta.Consumer}
		request.Header["C-Pending"] = []string{fmt.Sprintf("%d", meta.NumPending)}
		request.Header["C-Seq"] = []string{fmt.Sprintf("%d", meta.Sequence.Consumer)}
		request.Header["C-Attempt"] = []string{fmt.Sprintf("%d", max(1, meta.NumDelivered)-1)}

		request.Header["S-ID"] = []string{meta.Stream}
		request.Header["S-Seq"] = []string{fmt.Sprintf("%d", meta.Sequence.Stream)}
		request.Header["S-Domain"] = []string{meta.Domain}
		request.Header["S-Time"] = []string{strconv.FormatInt(meta.Timestamp.UnixMilli(), 10)}
	}

	// Serve the request
	request = vhttp.StartInternalRequest(request)
	for k, v := range headers {
		request.Header[k] = v
		if k == "P-Ip" && len(v) == 1 {
			request.RemoteAddr = v[0] + ":0"
		}
	}
	buf := vhttp.NewBufferedResponse(nil)
	t.Route.ServeHTTP(buf, request)

	// Handle the response
	paniced = false
	if 200 <= buf.Status && buf.Status < 299 {
		if buf.Status == 204 || buf.Status == 202 {
			return nil, nil
		}
		return buf.Body.Bytes(), nil
	}
	if buf.Status == 404 {
		return nil, errors.New("service not found, broken route")
	}
	if 400 <= buf.Status && buf.Status < 499 {
		return nil, retry.Disable(fmt.Errorf("HTTP %d: %s", buf.Status, buf.Body.String()))
	}
	return nil, fmt.Errorf("HTTP %d: %s", buf.Status, buf.Body.String())
}
func (t *Runner) ServeCore(ctx context.Context, gw *enats.Gateway, msg *nats.Msg) {
	logger := xlog.Ctx(ctx).With().Str("subject", msg.Subject).Str("reply", msg.Reply).Logger()
	logger.Debug().Msg("Task received")

	data, err := t.ServeMsg(ctx, msg.Subject, msg.Data, nil, msg.Header)
	if err != nil {
		xlog.Warn().Err(err).Msg("Failed to execute task")

		// If no reply is set, we don't need to do anything
		if msg.Reply == "" {
			return
		}

		// Not jetstream, so no ack/nak, just reply with the error
		msg.RespondMsg(&nats.Msg{
			Subject: msg.Reply,
			Data:    []byte(err.Error()),
			Header:  nats.Header{"Status": []string{"500"}},
		})
	} else {
		// If no reply is set, we don't need to do anything
		if msg.Reply == "" {
			return
		}

		// Respond with the result
		msg.Respond(data)
	}
}
func (t *Runner) ServeJetstream(ctx context.Context, gw *enats.Gateway, msg jetstream.Msg) {
	logger := xlog.Ctx(ctx).With().Str("subject", msg.Subject()).Str("reply", msg.Reply()).Logger()
	logger.Debug().Msg("Task received")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Create a ticker to declare in progress
	mu := &sync.Mutex{}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !mu.TryLock() {
					return
				}
				if err := msg.InProgress(); err != nil {
					logger.Warn().Err(err).Msg("Failed to declare in progress")
				}
				mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Execute the task and stop the ticker
	meta := lo.Must(msg.Metadata())
	data, err := t.ServeMsg(ctx, msg.Subject(), msg.Data(), meta, msg.Headers())
	mu.Lock()

	if err != nil {
		logger.Err(err).Msg("Failed to execute task")

		if retry.Retryable(err) {
			delay, err := t.Policy.WithDefaults().StepN(int(meta.NumDelivered) - 1)
			if err == nil {
				msg.NakWithDelay(delay)
				return
			}
		}

		if !t.NoDeadLetter {
			data, err := json.Marshal(map[string]interface{}{"error": err.Error()})
			if err == nil {
				_, err = gw.ResultKV.Put(
					context.Background(),
					fmt.Sprintf("%s-%d", meta.Stream, meta.Sequence.Stream),
					data,
				)
			}
			if err != nil {
				logger.Err(err).Msg("Failed to store result")
				msg.Nak()
				return
			}
		}
		msg.Term()
	} else {
		logger.Trace().Msg("Task completed")
		if len(data) != 0 {
			_, err := gw.ResultKV.Put(
				context.Background(),
				fmt.Sprintf("%s-%d", meta.Stream, meta.Sequence.Stream),
				data,
			)
			if err != nil {
				logger.Err(err).Msg("Failed to store result")
				msg.Nak()
				return
			}
		}
		msg.Ack()
	}
}

func consumeWrapper[T any](ctx context.Context, rate rate.Rate, fetch func() (T, error), serve func(T)) {
	left := uint(0)
	next := time.Time{}

	for ctx.Err() == nil {
		// If we're past the previous period, reset the counter
		now := time.Now()
		if next.Before(now) {
			next = now.Add(rate.Period)
			left = rate.Count
		}

		sleep := time.Time{}
		if left <= 0 {
			// If we have no quota left, sleep until the next period
			sleep = next
		} else if msg, err := fetch(); err != nil {
			// If we have quota left, but next errors, sleep for a bit
			sleep = now.Add(1 * time.Second)
		} else {
			// Run the message handler and decrement the quota
			go serve(msg)
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
}

func (t *Runner) ConsumeCore(ctx context.Context, gw *enats.Gateway, subj, queue string) (err error) {
	var sub *nats.Subscription
	if t.Rate.IsZero() {
		sub, err = gw.QueueSubscribe(subj, queue, func(msg *nats.Msg) {
			t.ServeCore(ctx, gw, msg)
		})
		if err != nil {
			return err
		}
	} else {
		sub, err = gw.QueueSubscribeSync(subj, queue)
		if err != nil {
			return err
		}
		go consumeWrapper(
			ctx,
			t.Rate,
			func() (msg *nats.Msg, err error) { return sub.NextMsgWithContext(ctx) },
			func(msg *nats.Msg) { t.ServeCore(ctx, gw, msg) },
		)
	}
	context.AfterFunc(ctx, func() { sub.Unsubscribe() })
	return nil
}
func (t *Runner) ConsumeJetstream(ctx context.Context, gw *enats.Gateway, cns jetstream.Consumer) error {
	if t.Rate.IsZero() {
		consumer, err := cns.Consume(func(msg jetstream.Msg) {
			t.ServeJetstream(ctx, gw, msg)
		})
		if err != nil {
			return err
		}
		context.AfterFunc(ctx, consumer.Stop)
	} else {
		go consumeWrapper(
			ctx,
			t.Rate,
			func() (jetstream.Msg, error) { return cns.Next(jetstream.FetchMaxWait(time.Minute)) },
			func(msg jetstream.Msg) { t.ServeJetstream(ctx, gw, msg) },
		)
	}
	return nil
}
func (t *Runner) Listen(ctx context.Context, gw *enats.Gateway, topic string) (cancel context.CancelFunc, err error) {
	ctx, cancel = context.WithCancel(ctx)
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	// Normalize the subject, resolve the stream.
	subj := enats.ToSubject(topic)
	queue := enats.ToConsumerQueueName("run-", topic)
	var streamName string
	if strings.HasPrefix(subj, enats.EventStreamPrefix) {
		streamName = gw.EventStream.CachedInfo().Config.Name
	} else {
		streamName, err = gw.Jet.StreamNameBySubject(ctx, subj)
	}

	if err == jetstream.ErrStreamNotFound {
		// If the stream does not exist, this is a core subject
		err = t.ConsumeCore(ctx, gw, subj, queue)
		if err != nil {
			err = fmt.Errorf("failed to consume core subject %q: %w", subj, err)
			return
		}
		xlog.Info().Str("subject", topic).Str("queue", queue).Msg("Core task listening")
	} else {
		var cns jetstream.Consumer

		// If the stream exists, this is a jetstream subject, resolve the stream and consumer
		stream := gw.EventStream
		if streamName != gw.EventStream.CachedInfo().Config.Name {
			stream, err = gw.Jet.Stream(ctx, streamName)
			if err != nil {
				err = fmt.Errorf("failed to resolve stream %q: %w", streamName, err)
				return
			}
		}
		cns, err = stream.Consumer(ctx, queue)
		if err == jetstream.ErrConsumerNotFound {
			cns, err = gw.EventStream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
				Durable:       queue,
				DeliverPolicy: jetstream.DeliverAllPolicy,
				AckPolicy:     jetstream.AckExplicitPolicy,
				ReplayPolicy:  jetstream.ReplayInstantPolicy,
				MaxDeliver:    -1,
				MaxAckPending: -1,
				FilterSubject: subj,
			})
		}
		if err != nil {
			err = fmt.Errorf("failed to resolve consumer for stream %q: %w", streamName, err)
			return
		} else if err = t.ConsumeJetstream(ctx, gw, cns); err != nil {
			err = fmt.Errorf("failed to consume jetstream subject %q: %w", subj, err)
			return
		}
		xlog.Info().Str("subject", topic).Str("queue", queue).Str("stream", streamName).Msg("Jetstream task listening")
	}

	for i, sch := range t.Schedule {
		go sch.Run(ctx, i, gw, topic, queue)
	}
	return cancel, nil
}
