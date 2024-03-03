package tlsmux

import (
	"crypto/tls"
	"net"
	"sync"
)

var sharedMuxMap = make(map[string]*Listener)
var sharedMuxLock sync.Mutex

// Listen starts a new listener on the given network, address and protocol using a shared multiplexer.
func Listen(network, address string, config *tls.Config, protos ...string) (net.Listener, error) {
	sharedMuxLock.Lock()
	defer sharedMuxLock.Unlock()

	// If a listener already exists for the given address, try to reuse it.
	if l, ok := sharedMuxMap[address]; ok {
		if !l.closed {
			ln, err := l.Listen(config, protos...)
			if err != ErrListenerClosed {
				return ln, err
			}
		}
		delete(sharedMuxMap, address)
	}

	// Create a new multiplexer listener.
	netListener, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	muxListener := NewMuxListener(netListener)
	muxListener.closeOnDrain = true

	// Start our instance, if we fail, close everything and abort.
	ln, err := muxListener.Listen(config, protos...)
	if err != nil {
		muxListener.Close()
		netListener.Close()
		return nil, err
	}

	// Store the listener in the shared map and return the listener.
	sharedMuxMap[address] = muxListener
	return ln, nil
}
