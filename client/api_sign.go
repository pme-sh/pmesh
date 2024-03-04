package client

import "get.pme.sh/pmesh/urlsigner"

func (c Client) SignURL(p urlsigner.Options) (res string, err error) {
	err = c.Call("/sign", p, &res)
	return
}
