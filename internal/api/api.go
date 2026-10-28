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
	"strconv"

	"github.com/tjst-t/sakutei-view/internal/aggregate"
	"github.com/tjst-t/sakutei-view/internal/source/plugin"
	"github.com/tjst-t/sakutei-view/internal/timeline"
)

// Aggregator is the subset of *aggregate.Aggregator this package needs.
// Defined as an interface so tests can supply a fixed stand-in without
// standing up real HTTP sources.
type Aggregator interface {
	Snapshot(ctx context.Context) (aggregate.Snapshot, error)

	// Events subscribes to the merged event feed. pluginResumeID, if
	// non-empty, is sent as the plugin's Last-Event-ID on its very first
	// connection (see aggregate.Aggregator.Events's doc comment) —
	// RunEventPump passes Timeline.LastPluginEventID() here so the plugin's
	// stream resumes across a process restart, not just a dropped
	// connection.
	Events(ctx context.Context, pluginResumeID string, onEvent func(aggregate.Event) error) error
}

// Server holds the handlers for /api/snapshot and /api/events.
type Server struct {
	Aggregator Aggregator

	// Plugin, if non-nil, is used to serve the write/action endpoints
	// (GET /api/scripts, POST /api/actions/{scriptID}/{action}, GET
	// /api/runs/{runID}). Those endpoints are only ever registered when
	// Plugin is non-nil AND Basic auth is configured below — see
	// RegisterRoutes and actionsEnabled.
	Plugin *plugin.Client

	// BasicAuthUser and BasicAuthPassword, if both non-empty, require every
	// request to this server's routes to present matching HTTP Basic
	// credentials. If either is empty, no auth is required.
	BasicAuthUser     string
	BasicAuthPassword string

	// Timeline, if non-nil, makes GET /api/events durable: a single
	// background subscription (started by calling RunEventPump once, from
	// cmd/sakutei-view) persists every event through it, and each browser's
	// own GET /api/events request replays its backlog (via Timeline.Since)
	// before switching to live delivery (via Timeline.Subscribe), with an
	// "id:" line on every SSE event so the browser's own EventSource can
	// resume with Last-Event-ID across a tab reload. If Timeline is nil,
	// handleEvents behaves exactly as it always has: a bare live pass-through
	// of Aggregator.Events with no backlog and no "id:" lines.
	Timeline *timeline.Store
}

// actionsEnabled reports whether the write/action endpoints are enabled:
// only when a plugin is configured AND Basic auth is configured. This
// keeps a deployment without Basic auth read-only by construction, not by
// an extra opt-out flag, and is also surfaced to the frontend as
// snapshotResponse.ActionsEnabled so it knows whether to render the
// operations panel without a separate capability probe.
func (s *Server) actionsEnabled() bool {
	return s.Plugin != nil && s.BasicAuthUser != "" && s.BasicAuthPassword != ""
}

// RegisterRoutes adds this server's routes to mux, wrapped in Basic auth if
// configured. GET /api/snapshot and GET /api/events are always registered.
// The write/action routes (GET /api/scripts, POST
// /api/actions/{scriptID}/{action}, GET /api/runs/{runID}) are registered
// only when actionsEnabled() is true; otherwise those paths are left
// unregistered entirely (a plain 404, not an explicit "disabled"
// response), so a deployment without both a plugin and Basic auth
// configured stays read-only.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/snapshot", s.protect(http.HandlerFunc(s.handleSnapshot)))
	mux.Handle("GET /api/events", s.protect(http.HandlerFunc(s.handleEvents)))

	if s.actionsEnabled() {
		mux.Handle("GET /api/scripts", s.protect(http.HandlerFunc(s.handleScripts)))
		mux.Handle("POST /api/actions/{scriptID}/{action}", s.protect(http.HandlerFunc(s.handleRunAction)))
		mux.Handle("GET /api/runs/{runID}", s.protect(http.HandlerFunc(s.handleGetRun)))
	}
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
	if err := json.NewEncoder(w).Encode(newSnapshotResponse(snap, s.actionsEnabled())); err != nil {
		log.Printf("api: /api/snapshot: encoding response: %v", err)
	}
}

// handleScripts serves GET /api/scripts: the plugin's available demo
// scripts and their actions, as JSON (proxies GET /plugin/scripts
// verbatim).
func (s *Server) handleScripts(w http.ResponseWriter, r *http.Request) {
	result, err := s.Plugin.Scripts(r.Context())
	if err != nil {
		s.writePluginError(w, "scripts", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("api: /api/scripts: encoding response: %v", err)
	}
}

// handleRunAction serves POST /api/actions/{scriptID}/{action}: starts (or
// reports the conflict of) a plugin run. It proxies POST
// /plugin/scripts/{scriptID}/actions/{action} and mirrors the plugin's own
// status code: 202 Accepted for a new run, 409 Conflict (with reason/
// busy_by in the body) if one was already in progress. The view never
// calls Sakutei's own approval/write endpoints directly — only this
// plugin-proxying path.
func (s *Server) handleRunAction(w http.ResponseWriter, r *http.Request) {
	scriptID := r.PathValue("scriptID")
	action := r.PathValue("action")
	result, err := s.Plugin.RunAction(r.Context(), scriptID, action)
	if err != nil {
		s.writePluginError(w, "actions", err)
		return
	}
	status := http.StatusAccepted
	if !result.Accepted {
		status = http.StatusConflict
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("api: /api/actions: encoding response: %v", err)
	}
}

// handleGetRun serves GET /api/runs/{runID}: the current status of one
// run, proxying GET /plugin/runs/{run_id} verbatim (including an unknown
// run id's status code, via writePluginError/plugin.StatusCodeOf).
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	run, err := s.Plugin.Run(r.Context(), runID)
	if err != nil {
		s.writePluginError(w, "runs", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(run); err != nil {
		log.Printf("api: /api/runs: encoding response: %v", err)
	}
}

// writePluginError translates an error from a *plugin.Client call into an
// HTTP response: if the plugin itself responded with a recognizable status
// code (via plugin.StatusCodeOf — e.g. a 404 for an unknown run id), that
// exact status is forwarded so the frontend can tell "no such run" apart
// from "the plugin is unreachable"; any other error (a transport failure,
// a timeout, ...) is reported as 502, matching handleSnapshot's treatment
// of aggregator failures.
func (s *Server) writePluginError(w http.ResponseWriter, what string, err error) {
	log.Printf("api: /api/%s: %v", what, err)
	if code, ok := plugin.StatusCodeOf(err); ok {
		http.Error(w, "plugin request failed", code)
		return
	}
	http.Error(w, "plugin request failed", http.StatusBadGateway)
}

// handleEvents serves GET /api/events: a Server-Sent Events stream of the
// aggregator's merged event feed, one JSON object per "data:" line.
//
// If Timeline is nil, this is a bare live pass-through of Aggregator.Events
// exactly as before this field existed: no backlog, no "id:" lines, one
// upstream subscription per connected browser tab.
//
// If Timeline is configured, each connection instead: (1) subscribes to
// Timeline's live feed first, (2) replays Timeline.Since(lastEventID) as
// backlog so a freshly opened or reloaded tab sees history without ever
// calling Aggregator.Events itself, then (3) forwards the live feed,
// skipping anything with Seq <= the last one already sent (the backlog may
// overlap the start of the live feed, since step 1 happens before step 2 to
// avoid missing an event that arrives in between). The actual upstream
// subscription (Aggregator.Events) is owned by RunEventPump, started once
// from cmd/sakutei-view, not by this handler — multiple browser tabs share
// it through Timeline rather than each opening their own.
//
// lastEventID comes from the Last-Event-ID request header, which browsers
// send automatically when an EventSource reconnects after a drop; it is
// parsed as the uint64 Timeline.Entry.Seq it names, and treated as absent
// (full backlog) if missing or unparsable.
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

	if s.Timeline == nil {
		err := s.Aggregator.Events(r.Context(), "", func(ev aggregate.Event) error {
			return writeSSEEvent(w, flusher, 0, ev)
		})
		if err != nil && r.Context().Err() == nil {
			log.Printf("api: /api/events: %v", err)
		}
		return
	}

	// Subscribe before reading the backlog (not after) so an event that
	// arrives in between is not lost — it will simply appear in both the
	// backlog and, redundantly, as the first live delivery, which lastSent
	// below filters back out.
	live, cancel := s.Timeline.Subscribe()
	defer cancel()

	var lastSent uint64
	var resumeFrom uint64
	if id := r.Header.Get("Last-Event-ID"); id != "" {
		if parsed, err := strconv.ParseUint(id, 10, 64); err == nil {
			resumeFrom = parsed
		}
	}
	for _, entry := range s.Timeline.Since(resumeFrom) {
		if err := writeSSEEvent(w, flusher, entry.Seq, entry.Event); err != nil {
			return
		}
		lastSent = entry.Seq
	}

	for {
		select {
		case entry, ok := <-live:
			if !ok {
				return
			}
			if entry.Seq <= lastSent {
				continue
			}
			if err := writeSSEEvent(w, flusher, entry.Seq, entry.Event); err != nil {
				return
			}
			lastSent = entry.Seq
		case <-r.Context().Done():
			return
		}
	}
}

// writeSSEEvent writes one event as an SSE "data:" line, preceded by an
// "id:" line when seq is non-zero (zero means "no Timeline configured" or
// "not yet assigned a sequence number" — omitting the id line in that case
// matches this handler's pre-Timeline behavior exactly, byte for byte).
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, seq uint64, ev aggregate.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}
	if seq > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", seq); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// RunEventPump is the single long-lived background subscription that makes
// GET /api/events durable: it persists every event from Aggregator.Events
// into Timeline, independently of whether any browser tab is currently
// connected. Without it, an event that occurred while no tab was open would
// never be recorded, breaking the "reloading/reopening the page still shows
// what happened while it was closed" requirement that Timeline exists for.
//
// It resumes the plugin's stream from Timeline.LastPluginEventID() on its
// very first connection, so a process restart does not redeliver (or, if
// the plugin does not retain that much backlog, lose) events the plugin
// already sent before the restart — see aggregate.Aggregator.Events's
// pluginResumeID parameter.
//
// RunEventPump blocks until ctx is cancelled or the Aggregator.Events call
// returns a non-context error (which, per Aggregator.Events's own contract,
// only happens if its onEvent callback returns an error — Append's own
// errors, e.g. a disk write failure, are logged and treated as "keep
// going" rather than stopping the whole pump, since one bad write should
// not stop every browser tab's live updates). It is a no-op if s.Timeline
// is nil. Call it exactly once, in its own goroutine, after constructing
// Server with Timeline set.
func (s *Server) RunEventPump(ctx context.Context) error {
	if s.Timeline == nil {
		return nil
	}
	err := s.Aggregator.Events(ctx, s.Timeline.LastPluginEventID(), func(ev aggregate.Event) error {
		if _, _, err := s.Timeline.Append(ev); err != nil {
			log.Printf("api: event pump: persisting event: %v", err)
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		log.Printf("api: event pump: %v", err)
	}
	return err
}
