package util

import (
	"io"
	"time"
)

var drainBuffer = make([]byte, 4192)

// always closed: 20k -> 8 seconds
// never closed:  20k -> 200ms
// assuming we lose ~1s per connection and 10k clients
// we should close any given reader after 5s of stalling for normal-exit
func DrainClose(r io.ReadCloser) {
	if r == nil {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	defer r.Close()
	for {
		_, err := r.Read(drainBuffer)
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			break
		}
	}
}
