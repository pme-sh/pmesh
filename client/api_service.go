package client

import (
	"github.com/pme-sh/pmesh/session"
	"github.com/pme-sh/pmesh/snowflake"
)

func (c Client) ServiceHealth(name string) (h session.ServiceHealth, err error) {
	err = c.Call("/service/health/"+name, nil, &h)
	return
}
func (c Client) ServiceMetrics(name string) (m session.ServiceMetrics, err error) {
	err = c.Call("/service/metrics/"+name, nil, &m)
	return
}
func (c Client) ServiceHealthMap() (h map[string]session.ServiceHealth, err error) {
	err = c.Call("/service/health", nil, &h)
	return
}
func (c Client) ServiceMetricsMap() (m map[string]session.ServiceMetrics, err error) {
	err = c.Call("/service/metrics", nil, &m)
	return
}
func (c Client) ServiceInfo(name string) (m session.ServiceInfo, err error) {
	err = c.Call("/service/info/"+name, nil, &m)
	return
}
func (c Client) ServiceInfoMap() (m map[string]session.ServiceInfo, err error) {
	err = c.Call("/service/info", nil, &m)
	return
}
func (c Client) ServiceRestart(name string, invalidate bool) (res session.ServiceCommandResult, err error) {
	err = c.Call("/service/restart/"+name, session.ServiceInvalidate{Invalidate: invalidate}, &res)
	return
}
func (c Client) ServiceRestartAll(invalidate bool) (res session.ServiceCommandResult, err error) {
	err = c.Call("/service/restart", session.ServiceInvalidate{Invalidate: invalidate}, &res)
	return
}
func (c Client) ServiceStop(name string) (res session.ServiceCommandResult, err error) {
	err = c.Call("/service/stop/"+name, nil, &res)
	return
}
func (c Client) ServiceStopAll() (res session.ServiceCommandResult, err error) {
	err = c.Call("/service/stop", nil, &res)
	return
}
func (c Client) Services() (res map[string]snowflake.ID, err error) {
	err = c.Call("/service", nil, &res)
	return
}
