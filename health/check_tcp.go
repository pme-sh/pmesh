package health

import (
	"context"
	"errors"
	"time"

	"get.pme.sh/pmesh/netx"
)

type TcpCheck struct{}

func (t *TcpCheck) UnmarshalInline(text string) error {
	if text == "TCP" {
		return nil
	}
	return errors.New("not a TCP check")
}

func (t *TcpCheck) Perform(ctx context.Context, addr string) error {
	var dialer netx.LocalDialer
	dialer.Timeout = 15 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Timeout = min(dialer.Timeout, time.Until(deadline))
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}
func init() {
	Registry.Define("Tcp", func() any {
		return &TcpCheck{}
	})
}
