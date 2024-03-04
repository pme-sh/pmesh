package session

import (
	"encoding/json"
	"fmt"
	"net/http"

	"get.pme.sh/pmesh/xlog"
)

func init() {
	Match("POST /tail", func(s *Session, r *http.Request, w http.ResponseWriter) (res struct{}, err error) {
		// Read the tail options.
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var opt xlog.TailOptions
		err = dec.Decode(&opt)
		if err != nil {
			return
		}

		// Hijack the connection and start tailing.
		rc := http.NewResponseController(w)
		conn, bwr, err := rc.Hijack()
		if err != nil {
			err = fmt.Errorf("failed enable server-sent events: %w", err)
			return
		}
		defer conn.Close()

		bwr.Write([]byte("HTTP/1.1 200 OK\r\n"))
		bwr.Write([]byte("Content-Type: application/stream+json\r\n"))
		bwr.Write([]byte("Cache-Control: no-cache\r\n"))
		bwr.Write([]byte("Connection: keep-alive\r\n"))
		bwr.Write([]byte("\r\n"))
		bwr.Flush()

		err = xlog.TailContext(s.Context, opt, conn)
		if err != nil {
			// If the connection is still fine, display an error message, must be an internal error.
			if _, wr := conn.Write([]byte(`\n`)); wr != nil {
				xlog.Info().Err(err).Msg("Tail terminated abnormally")
			}
		}
		err = http.ErrAbortHandler
		return
	})
}
