package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	router := http.NewServeMux()
	router.HandleFunc("GET /hi", func(w http.ResponseWriter, r *http.Request) {
		ray := r.Header.Get("X-Ray")
		log := slog.With("ray", ray)
		log.Info("Got request", "path", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		n, err := w.Write([]byte(`{"hello": "` + ray + `"}`))
		log.Info("Sent response", "n", n, "err", err)
		log.Info("Query was", "query", r.URL.RawQuery)
	})

	// Start the server
	listener, err := net.Listen("tcp4", os.Getenv("LISTEN"))
	if err != nil {
		panic(err)
	}

	go func() {
		for range time.Tick(5 * time.Second) {
			fmt.Println("hi")
		}
	}()

	sv := &http.Server{Handler: router}
	slog.Info("Server listening", "addr", listener.Addr())
	sv.Serve(listener)
}
