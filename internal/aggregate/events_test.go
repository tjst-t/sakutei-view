package aggregate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tjst-t/sakutei-view/internal/source/plugin"
	"github.com/tjst-t/sakutei-view/internal/source/sakutei"
)

const sakuteiEventsFixture = `data: {"time":"2026-10-28T00:00:00Z","kind":"drift","severity":"warning","resource":"leaf1","message":"leaf1 drifted","state":"firing"}

data: {"time":"2026-10-28T00:01:00Z","kind":"drift","severity":"warning","resource":"leaf1","message":"leaf1 drift resolved","state":"resolved"}

`

const pluginEventsFixture = `id: 1
data: {"kind":"restart","level":"firing","resource_id":"node3","message":"node3 restarted","at":"2026-10-28T00:02:00Z"}

`

// errStop is a sentinel onEvent error used throughout these tests to end an
// Events call deterministically once the expected events have arrived.
// This matters now that Events reconnects dropped/finished streams forever
// (see events.go) — a test fixture server that sends a fixed batch and then
// closes the connection looks exactly like a transient drop, so without a
// stop signal the aggregator would just keep reconnecting and redelivering
// it for as long as the test's context allows.
var errStop = &stopError{}

type stopError struct{}

func (*stopError) Error() string { return "stop" }

func TestAggregator_Events_MergesBothSources(t *testing.T) {
	sakuteiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(sakuteiEventsFixture))
	}))
	defer sakuteiSrv.Close()

	pluginMux := http.NewServeMux()
	pluginMux.HandleFunc("GET /plugin/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(pluginEventsFixture))
	})
	pluginSrv := httptest.NewServer(pluginMux)
	defer pluginSrv.Close()

	cl := mockContainerlabSource{}
	agg := New(cl, sakutei.NewClient(sakuteiSrv.URL, nil), plugin.NewClient(pluginSrv.URL, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got []Event
	err := agg.Events(ctx, func(ev Event) error {
		got = append(got, ev)
		if len(got) == 3 {
			return errStop
		}
		return nil
	})
	if err != errStop {
		t.Fatalf("Events: %v, want errStop", err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(got), got)
	}

	var sakuteiCount, pluginCount int
	for _, ev := range got {
		switch ev.Source {
		case EventSourceSakutei:
			sakuteiCount++
			if ev.Resource != "leaf1" || ev.Kind != "drift" || ev.Severity != "warning" {
				t.Errorf("unexpected sakutei event: %+v", ev)
			}
		case EventSourcePlugin:
			pluginCount++
			if ev.Resource != "node3" || ev.Kind != "restart" || ev.State != "firing" || ev.Severity != "" {
				t.Errorf("unexpected plugin event: %+v", ev)
			}
			if ev.Time.IsZero() {
				t.Errorf("plugin event has zero Time: %+v", ev)
			}
		default:
			t.Errorf("unexpected event source: %+v", ev)
		}
	}
	if sakuteiCount != 2 || pluginCount != 1 {
		t.Errorf("sakuteiCount=%d pluginCount=%d, want 2 and 1", sakuteiCount, pluginCount)
	}
}

func TestAggregator_Events_NoSourcesConfigured(t *testing.T) {
	agg := New(mockContainerlabSource{}, nil, nil)
	called := false
	err := agg.Events(context.Background(), func(Event) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if called {
		t.Error("onEvent was called with no sources configured")
	}
}

func TestAggregator_Events_OnEventErrorStopsBoth(t *testing.T) {
	// A slow, never-ending sakutei stream paired with a plugin stream that
	// delivers one event immediately; onEvent returning an error on the
	// first event must stop the whole call rather than block forever.
	sakuteiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if ok {
			flusher.Flush()
		}
		<-r.Context().Done() // hang until the client disconnects
	}))
	defer sakuteiSrv.Close()

	pluginMux := http.NewServeMux()
	pluginMux.HandleFunc("GET /plugin/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(pluginEventsFixture))
	})
	pluginSrv := httptest.NewServer(pluginMux)
	defer pluginSrv.Close()

	cl := mockContainerlabSource{}
	agg := New(cl, sakutei.NewClient(sakuteiSrv.URL, nil), plugin.NewClient(pluginSrv.URL, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := agg.Events(ctx, func(ev Event) error {
		return errStop
	})
	if err != errStop {
		t.Fatalf("Events error = %v, want errStop", err)
	}
}

// TestAggregator_Events_PluginReconnectsWithLastEventID drops the plugin's
// SSE connection right after the first event and verifies the aggregator
// reconnects, sending the last event ID it saw as Last-Event-ID, and picks
// up with the next event rather than missing or repeating it.
func TestAggregator_Events_PluginReconnectsWithLastEventID(t *testing.T) {
	var mu sync.Mutex
	var receivedLastEventIDs []string

	pluginMux := http.NewServeMux()
	pluginMux.HandleFunc("GET /plugin/events", func(w http.ResponseWriter, r *http.Request) {
		lastID := r.Header.Get("Last-Event-ID")
		mu.Lock()
		receivedLastEventIDs = append(receivedLastEventIDs, lastID)
		mu.Unlock()

		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		if lastID == "" {
			// First connection: send event 1, then the handler returns,
			// ending the response — simulating the connection dropping
			// before event 2 was ever sent.
			ev := plugin.Event{Kind: "x", Level: "firing", ResourceID: "r", Message: "one", At: time.Now().UTC()}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "id: 1\ndata: %s\n\n", b)
			flusher.Flush()
			return
		}

		if lastID != "1" {
			t.Errorf("reconnect Last-Event-ID = %q, want \"1\"", lastID)
		}
		ev := plugin.Event{Kind: "x", Level: "firing", ResourceID: "r", Message: "two", At: time.Now().UTC()}
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "id: 2\ndata: %s\n\n", b)
		flusher.Flush()
		<-r.Context().Done() // stay open; don't let a third reconnect muddy the test
	})
	pluginSrv := httptest.NewServer(pluginMux)
	defer pluginSrv.Close()

	cl := mockContainerlabSource{}
	agg := New(cl, nil, plugin.NewClient(pluginSrv.URL, nil))
	agg.ReconnectInitial = 5 * time.Millisecond
	agg.ReconnectMax = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got []Event
	err := agg.Events(ctx, func(ev Event) error {
		got = append(got, ev)
		if len(got) == 2 {
			return errStop
		}
		return nil
	})
	if err != errStop {
		t.Fatalf("Events: %v, want errStop", err)
	}
	if len(got) != 2 || got[0].Message != "one" || got[1].Message != "two" {
		t.Fatalf("got = %+v, want messages [one, two]", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(receivedLastEventIDs) < 2 || receivedLastEventIDs[0] != "" || receivedLastEventIDs[1] != "1" {
		t.Fatalf("receivedLastEventIDs = %v, want first \"\" then \"1\"", receivedLastEventIDs)
	}
}

// TestAggregator_Events_OneSourceDownOtherContinues verifies that Sakutei
// being completely unreachable (connection refused on every attempt) does
// not stop the plugin's event stream from being delivered.
func TestAggregator_Events_OneSourceDownOtherContinues(t *testing.T) {
	// A server that is immediately closed still has a valid-looking URL;
	// connecting to it fails with "connection refused" on every attempt,
	// simulating a permanently unreachable source without hanging.
	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := deadSrv.URL
	deadSrv.Close()

	pluginMux := http.NewServeMux()
	pluginMux.HandleFunc("GET /plugin/events", func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 1; i <= 2; i++ {
			ev := plugin.Event{Kind: "x", Level: "firing", ResourceID: "r", Message: fmt.Sprintf("m%d", i), At: time.Now().UTC()}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", i, b)
			flusher.Flush()
		}
		<-r.Context().Done()
	})
	pluginSrv := httptest.NewServer(pluginMux)
	defer pluginSrv.Close()

	cl := mockContainerlabSource{}
	agg := New(cl, sakutei.NewClient(deadURL, nil), plugin.NewClient(pluginSrv.URL, nil))
	agg.ReconnectInitial = 5 * time.Millisecond
	agg.ReconnectMax = 20 * time.Millisecond
	agg.RequestTimeout = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got []Event
	err := agg.Events(ctx, func(ev Event) error {
		got = append(got, ev)
		if len(got) == 2 {
			return errStop
		}
		return nil
	})
	if err != errStop {
		t.Fatalf("Events: %v, want errStop", err)
	}
	for _, ev := range got {
		if ev.Source != EventSourcePlugin {
			t.Errorf("got an event from source %q, want only plugin events (sakutei is unreachable)", ev.Source)
		}
	}

	h := agg.healthSnapshot()[SourceSakutei]
	if h.OK || h.LastError == "" {
		t.Errorf("Sakutei health = %+v, want OK=false with a LastError recorded", h)
	}
}
