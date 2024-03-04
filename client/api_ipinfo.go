package client

import "get.pme.sh/pmesh/session"

func (c Client) QueryIP(ip string) (info session.IPInfoResult, err error) {
	err = c.Call("/ipinfo", session.IPInfoQuery{IP: ip}, &info)
	return
}
