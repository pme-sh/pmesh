package client

func (c Client) Ping() (res string, err error) {
	err = c.Call("/ping", nil, &res)
	return
}
