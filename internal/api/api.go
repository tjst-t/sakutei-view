// Package api serves sakutei-view's own HTTP API: a REST snapshot endpoint
// (GET /api/snapshot) for the browser's initial load, and a Server-Sent
// Events stream (GET /api/events) for live updates afterward. Both are
// backed by an Aggregator (normally *internal/aggregate.Aggregator).
//
// The server does not decide where to listen or whether that address is
// reachable beyond localhost — that is cmd/sakutei-view's job (it defaults
// to a localhost-only address; exposing it further is a separate, explicit
// choice made outside this package). This package only optionally adds
// HTTP Basic auth in front of its own two routes, for deployments that do
// expose it beyond localhost.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/tjst-t/sakutei-view/internal/aggregate"
)

// Aggregator is the subset of *aggregate.Aggregator this package needs.
// Defined as an interface so tests can supply a fixed stand-in without
// standing up real HTTP sources.
type Aggregator interface {
	Snapshot(ctx context.Context) (aggregate.Snapshot, error)
	Events(ctx context.Context, onEvent func(aggregate.Event) error) error
}

// Server holds the handlers for /api/snapshot and /api/events.
type Server struct {
	Aggregator Aggregator

	// BasicAuthUser and BasicAuthPassword, if both non-empty, require every
	// request to this server's routes to present matching HTTP Basic
	// credentials. If either is empty, no auth is required.
	BasicAuthUser     string
	BasicAuthPassword string
}

// RegisterRoutes adds this server's routes (GET /api/snapshot, GET
// /api/events) to mux, wrapped in Basic auth if configured.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/snapshot", s.protect(http.HandlerFunc(s.handleSnapshot)))
	mux.Handle("GET /api/events", s.protect(http.HandlerFunc(s.handleEvents)))
}

func (s *Server) protect(next http.Handler) http.Handler {
	if s.BasicAuthUser == "" || s.BasicAuthPassword == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.BasicAuthUser)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.BasicAuthPassword)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="sakutei-view"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleSnapshot serves GET /api/snapshot: the aggregator's current
// snapshot, as JSON.
func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.Aggregator.Snapshot(r.Context())
	if err != nil {
		log.Printf("api: /api/snapshot: %v", err)
		http.Error(w, "fetching snapshot failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(newSnapshotResponse(snap)); err != nil {
		log.Printf("api: /api/snapshot: encoding response: %v", err)
	}
}

// handleEvents serves GET /api/events: a Server-Sent Events stream of the
// aggregator's merged event feed, one JSON object per "data:" line. It
// blocks until the client disconnects or the aggregator's stream ends.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	err := s.Aggregator.Events(r.Context(), func(ev aggregate.Event) error {
		data, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("encoding event: %w", err)
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil && r.Context().Err() == nil {
		log.Printf("api: /api/events: %v", err)
	}
}
