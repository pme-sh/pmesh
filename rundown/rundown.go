package rundown

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

var Signal = make(chan struct{})

func init() {
	go func() {
		var stopChan = make(chan os.Signal, 2)
		signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
		<-stopChan
		close(Signal)
	}()
}

func WithContext(rctx context.Context) (ctx context.Context, cancel context.CancelFunc) {
	ctx, cancel = context.WithCancel(rctx)
	go func() {
		<-Signal
		cancel()
	}()
	return
}
