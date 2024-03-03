package client

import "github.com/pme-sh/pmesh/session"

func (c Client) SystemMetrics() (m session.SystemMetrics, err error) {
	err = c.Call("/system", nil, &m)
	return
}
func (c Client) SessionMetrics() (m session.SessionMetrics, err error) {
	err = c.Call("/session", nil, &m)
	return
}
