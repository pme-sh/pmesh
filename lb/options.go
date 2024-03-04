package lb

import (
	"get.pme.sh/pmesh/rate"
	"get.pme.sh/pmesh/retry"
	"get.pme.sh/pmesh/util"
	"get.pme.sh/pmesh/vhttp"
)

type Strategy uint8

const (
	StrategyLeastConn Strategy = iota
	StrategyRandom
	StrategyHash
	StrategyRoundRobin
)

var StrategyEnum = util.NewEnum(map[Strategy]string{
	StrategyLeastConn:  "least",
	StrategyRandom:     "random",
	StrategyHash:       "hash",
	StrategyRoundRobin: "round-robin",
})

func (e Strategy) String() string                        { return StrategyEnum.ToString(e) }
func (e Strategy) MarshalText() (text []byte, err error) { return StrategyEnum.MarshalText(e) }
func (e *Strategy) UnmarshalText(text []byte) error      { return StrategyEnum.UnmarshalText(e, text) }

type StateType uint8

const (
	StateSticky StateType = iota
	StateNone
)

var StateEnum = util.NewEnum(map[StateType]string{
	StateSticky: "sticky",
	StateNone:   "none",
})

func (e StateType) String() string                        { return StateEnum.ToString(e) }
func (e StateType) MarshalText() (text []byte, err error) { return StateEnum.MarshalText(e) }
func (e *StateType) UnmarshalText(text []byte) error      { return StateEnum.UnmarshalText(e, text) }

type ErrorOptions struct {
	Handle *vhttp.Subhandler `yaml:"handle,omitempty"` // The error handler.
	Limit  rate.Limit        `yaml:"limit,omitempty"`  // The rate limit.
}

type Options struct {
	Retry    retry.Policy  `yaml:",inline"`         // The retry policy.
	Strategy Strategy      `yaml:"strat,omitempty"` // The load balancing strategy.
	State    StateType     `yaml:"state,omitempty"` // The session kind.
	Error4xx *ErrorOptions `yaml:"4xx,omitempty"`   // The error handler for 4xx responses.
	Error5xx *ErrorOptions `yaml:"5xx,omitempty"`   // The error handler for 5xx responses.
	Error404 *ErrorOptions `yaml:"404,omitempty"`   // The error handler for 404 responses.
}
