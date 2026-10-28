package aggregate

import (
	"context"
	"net/http"
	"net/http/httptest"
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
		return nil
	})
	if err != nil {
		t.Fatalf("Events: %v", err)
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

var errStop = &stopError{}

type stopError struct{}

func (*stopError) Error() string { return "stop" }
