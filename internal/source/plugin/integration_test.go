package plugin

import (
	"context"
	"net"
	"net/http"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestClient_AgainstExamplePlugin runs the real examples/example-plugin
// binary (not a mock) and exercises the full client against it: discovery,
// status, triggering a demo action, polling the run to completion, and
// resuming the event stream with Last-Event-ID.
func TestClient_AgainstExamplePlugin(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a subprocess; skipped in -short")
	}

	addr := freeAddr(t)
	baseURL := "http://" + addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// "go run" execs the built binary as a child of its own process; killing
	// just the "go run" process (what CommandContext does on cancel) leaves
	// that child running. Put the whole thing in its own process group so
	// cleanup can kill the group.
	cmd := exec.Command("go", "run", "./examples/example-plugin")
	cmd.Dir = "../../.."
	cmd.Env = append(cmd.Environ(), "LISTEN_ADDR="+addr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting example-plugin: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()
	})

	c := NewClient(baseURL, http.DefaultClient)
	waitForReady(t, c)

	info, err := c.Info(ctx)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name == "" || !info.HasCapability("status") {
		t.Fatalf("unexpected Info from example-plugin: %+v", info)
	}

	before, err := c.Status(ctx)
	if err != nil {
		t.Fatalf("Status (before): %v", err)
	}
	if len(before.Resources) == 0 {
		t.Fatal("expected example-plugin to report at least one resource")
	}
	resourceID := before.Resources[0].ID

	scripts, err := c.Scripts(ctx)
	if err != nil {
		t.Fatalf("Scripts: %v", err)
	}
	if len(scripts.Scripts) == 0 {
		t.Fatal("expected example-plugin to offer at least one script")
	}
	scriptID := scripts.Scripts[0].ID

	// Start listening for events (including the one the action below will
	// cause) before triggering the action, so we don't race the plugin.
	eventCtx, stopEvents := context.WithTimeout(ctx, 10*time.Second)
	defer stopEvents()
	eventsErr := make(chan error, 1)
	firstEventID := make(chan string, 1)
	go func() {
		eventsErr <- c.Events(eventCtx, "", func(id string, ev Event) error {
			select {
			case firstEventID <- id:
			default:
			}
			return nil
		})
	}()

	result, err := c.RunAction(ctx, scriptID, "disturb")
	if err != nil {
		t.Fatalf("RunAction(disturb): %v", err)
	}
	if !result.Accepted {
		t.Fatalf("expected disturb action to be accepted, got: %+v", result)
	}

	run := pollRunToCompletion(t, c, result.RunID)
	if run.Status != "succeeded" {
		t.Fatalf("expected run to succeed, got: %+v", run)
	}

	after, err := c.Status(ctx)
	if err != nil {
		t.Fatalf("Status (after): %v", err)
	}
	found := false
	for _, r := range after.Resources {
		if r.ID == resourceID && r.State == "drifted" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected resource %q to be drifted after disturb, got: %+v", resourceID, after.Resources)
	}

	var gotFirstID string
	select {
	case gotFirstID = <-firstEventID:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the disturb event on /plugin/events")
	}
	stopEvents()
	<-eventsErr // let the Events goroutine finish (context cancelled is expected)

	// Resume from the first event's id: should not re-deliver it.
	resumeCtx, stopResume := context.WithTimeout(ctx, 2*time.Second)
	defer stopResume()
	sawRepeated := false
	_ = c.Events(resumeCtx, gotFirstID, func(id string, ev Event) error {
		if id == gotFirstID {
			sawRepeated = true
		}
		return nil
	})
	if sawRepeated {
		t.Fatalf("resuming with Last-Event-ID=%q should not redeliver that event", gotFirstID)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func waitForReady(t *testing.T, c *Client) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err := c.Info(ctx)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("example-plugin did not become ready in time")
}

func pollRunToCompletion(t *testing.T, c *Client, runID string) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, err := c.Run(context.Background(), runID)
		if err != nil {
			t.Fatalf("Run(%q): %v", runID, err)
		}
		if run.Status != "running" {
			return run
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %q did not complete in time", runID)
	return Run{}
}
