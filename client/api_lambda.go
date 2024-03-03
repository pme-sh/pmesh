package client

import (
	"github.com/pme-sh/pmesh/session"
)

func (c Client) Lambdas() (res map[string]session.Lambda, err error) {
	err = c.Call("/lambda", nil, &res)
	return
}
