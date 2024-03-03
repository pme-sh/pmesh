package tlsmux

import (
	"crypto/tls"
	"net"
	"time"
)

func Client(netconn net.Conn, config *tls.Config, proto string) *tls.Conn {
	if config == nil {
		config = &tls.Config{}
	}
	config = config.Clone()
	config.NextProtos = []string{proto}
	return tls.Client(netconn, config)
}

func DialTimeout(network, address string, to time.Duration, config *tls.Config, proto string) (net.Conn, error) {
	netconn, err := net.DialTimeout(network, address, to)
	if err != nil {
		return nil, err
	}
	return Client(netconn, config, proto), nil
}

func Dial(network, address string, config *tls.Config, proto string) (net.Conn, error) {
	netconn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return Client(netconn, config, proto), nil
}
