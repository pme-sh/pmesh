package retry

import (
	"time"

	"get.pme.sh/pmesh/util"
)

const RetrierMaxDelayCoeff = 20

type Policy struct {
	Attempts int           `json:"attempts,omitempty" yaml:"attempts,omitempty"` // The maximum number of retries.
	Backoff  util.Duration `json:"backoff,omitempty" yaml:"backoff,omitempty"`   // The base delay between retries.
	Timeout  util.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`   // The maximum time to wait for a retry.
}

func Basic() Policy {
	return Policy{
		Attempts: 8,
		Backoff:  util.Duration(500 * time.Millisecond),
		Timeout:  util.Duration(30 * time.Second),
	}
}
func Long() Policy {
	return Policy{
		Attempts: -1,
		Backoff:  util.Duration(5 * time.Second),
		Timeout:  util.Duration(24 * time.Hour),
	}
}

func (retry Policy) WithDefaults() Policy {
	if retry.Attempts == 0 {
		retry.Attempts = 8
	}
	retry.Backoff = retry.Backoff.Or(150 * time.Millisecond)
	retry.Timeout = retry.Timeout.Or(30 * time.Second)
	return retry
}

func (retry Policy) MaxDelay() time.Duration {
	return retry.Backoff.Duration() * RetrierMaxDelayCoeff
}

func (retry Policy) StepN(n int) (delay time.Duration, err error) {
	if retry.Attempts > 0 && n >= retry.Attempts {
		return 0, ErrMaxAttemptsExceeded
	}
	delay = retry.Backoff.Duration()
	maxdelay := retry.MaxDelay()
	for ; n > 0; n-- {
		delay += retry.Backoff.Duration()
		delay = delay + (delay >> 1) // Exponential backoff
		if delay > maxdelay {
			return maxdelay, nil
		}
	}
	return
}

func (retry Policy) Step(step *int, delay *time.Duration) error {
	// First check if we should retry at all.
	n := *step
	*step = n + 1
	if retry.Attempts > 0 && n >= retry.Attempts {
		return ErrMaxAttemptsExceeded
	}

	// Update the delay and return
	if n == 0 {
		*delay = retry.Backoff.Duration()
	} else {
		*delay += retry.Backoff.Duration()
		*delay = *delay + (*delay >> 1) // Exponential backoff
		*delay = min(*delay, retry.MaxDelay())
	}
	return nil
}
