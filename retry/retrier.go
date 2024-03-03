package retry

import (
	"context"
	"errors"
	"time"
)

type Retrier struct {
	Context  context.Context
	Policy   Policy
	Step     int
	Delay    time.Duration
	Deadline time.Time
}

const RetrierMaxDelayCoeff = 20

// Creates a retries with the given policy and context
func (policy Policy) RetrierContext(ctx context.Context) Retrier {
	rt := Retrier{
		Context: ctx,
		Policy:  policy.adjust(),
	}
	rt.Deadline = time.Now().Add(rt.Policy.Timeout.Duration())
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(rt.Deadline) {
		rt.Deadline = deadline
	}
	return rt
}

// Creates a retries with the given policy
func (policy Policy) Retrier() Retrier {
	return policy.RetrierContext(context.Background())
}

// Returns the next delay, or an error if the retries are exhausted
func (retry *Retrier) NextDelay() (delay time.Duration, err error) {
	err = retry.Policy.Step(&retry.Step, &retry.Delay)
	if err == nil {
		retry.Delay = min(retry.Delay, retry.Policy.Backoff.Duration()*RetrierMaxDelayCoeff)
		delay = retry.Delay
	}
	return
}

// Tries to consume the error, and if it can't returns the error
func (retry *Retrier) ConsumeAny() error {
	// If we can't retry, return the error
	delay, retryErr := retry.NextDelay()
	if retryErr != nil {
		return retryErr
	}

	// If the context is done, return the error
	if e := retry.Context.Err(); e != nil {
		return e
	}

	// If the deadline is exceeded after the delay, return the error
	if timeLeft := time.Until(retry.Deadline); timeLeft < delay {
		if timeLeft > retry.Policy.Timeout.Duration() {
			delay = timeLeft >> 1
		} else {
			return ErrDeadlineExceeded
		}
	}

	select {
	case <-retry.Context.Done():
		return retry.Context.Err()
	case <-time.After(delay):
		return nil
	}
}
func (retry *Retrier) Consume(err error) error {
	if !Retryable(err) {
		return err
	}
	e := retry.ConsumeAny()
	if e != nil {
		return errors.Join(e, err)
	}
	return nil
}

// Runs the function until it succeeds or the retries are exhausted
func (retry Retrier) Run(f func() error) (err error) {
	for {
		// Run the function, and if it succeeds return
		if err = f(); err == nil {
			break
		}
		// Try to consume the error, and if we can't return
		if err = retry.Consume(err); err != nil {
			break
		}
	}
	return
}

// Convenience function for running a function with a retry policy
func (policy Policy) Run(f func() error) (err error) {
	return policy.Retrier().Run(f)
}

// Convenience function for running a function with a retry policy and context
func (policy Policy) RunContext(ctx context.Context, f func() error) (err error) {
	return policy.RetrierContext(ctx).Run(f)
}
