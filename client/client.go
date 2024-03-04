package client

import (
	"get.pme.sh/pmesh/pmtp"
)

type Client struct {
	pmtp.Client
	URL string
}

func (c Client) Valid() bool {
	return c.Client != nil
}

func ConnectTo(url string) (c Client, err error) {
	conn, err := pmtp.Connect(url)
	if err == nil {
		c = Client{conn, url}
	}
	return
}
func Connect() (c Client, err error) {
	return ConnectTo(pmtp.DefaultURL)
}
