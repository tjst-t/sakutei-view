// Command sakutei-view runs the sakutei-view server: it reads containerlab,
// Sakutei, and (optionally) a device plugin over HTTP, and serves a single
// read-only web page summarizing all three. See README.md.
//
// This is an early scaffold: the HTTP sources are not wired up yet.
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080" // localhost-only by default, per design
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("sakutei-view listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
