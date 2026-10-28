package httpclient

import (
	"bufio"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestTLSServer starts an httptest TLS server and writes its self-signed
// certificate out as a PEM file, suitable for use as a Config.CAFile.
func newTestTLSServer(t *testing.T, handler http.HandlerFunc) (srv *httptest.Server, caFile string) {
	t.Helper()
	srv = httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	cert := srv.Certificate()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})

	dir := t.TempDir()
	caFile = filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatalf("writing CA file: %v", err)
	}
	return srv, caFile
}

func TestNew_HTTPSWithCAFile_Succeeds(t *testing.T) {
	var gotAuth string
	srv, caFile := newTestTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	t.Setenv("TEST_BEARER_TOKEN", "s3cr3t")
	client, err := New(Config{
		BaseURL:  srv.URL,
		TokenEnv: "TEST_BEARER_TOKEN",
		CAFile:   caFile,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET with CA file configured should succeed, got: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotAuth != "Bearer s3cr3t" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer s3cr3t")
	}
}

func TestNew_WithoutCAFile_FailsVerification(t *testing.T) {
	srv, _ := newTestTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	client, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Get(srv.URL)
	if err == nil {
		t.Fatal("GET without CAFile should fail certificate verification, got nil error")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expected a certificate verification error, got: %v", err)
	}
}

func TestNew_SSEStreamOverHTTPS(t *testing.T) {
	srv, caFile := newTestTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < 3; i++ {
			_, _ = w.Write([]byte("id: " + string(rune('0'+i)) + "\ndata: hello\n\n"))
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	})

	client, err := New(Config{BaseURL: srv.URL, CAFile: caFile})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET (SSE) with CA file configured should succeed, got: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading SSE stream: %v", err)
	}

	joined := strings.Join(lines, "\n")
	if strings.Count(joined, "data: hello") != 3 {
		t.Fatalf("expected 3 SSE events, got stream:\n%s", joined)
	}
}

func TestConfig_Token(t *testing.T) {
	t.Run("no source configured", func(t *testing.T) {
		tok, err := Config{}.Token()
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		if tok != "" {
			t.Fatalf("Token = %q, want empty", tok)
		}
	})

	t.Run("from file, trims whitespace", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "token")
		if err := os.WriteFile(path, []byte("  abc123\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		tok, err := Config{TokenFile: path}.Token()
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		if tok != "abc123" {
			t.Fatalf("Token = %q, want %q", tok, "abc123")
		}
	})

	t.Run("from env", func(t *testing.T) {
		t.Setenv("HTTPCLIENT_TEST_TOKEN", "xyz")
		tok, err := Config{TokenEnv: "HTTPCLIENT_TEST_TOKEN"}.Token()
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		if tok != "xyz" {
			t.Fatalf("Token = %q, want %q", tok, "xyz")
		}
	})

	t.Run("env not set is an error", func(t *testing.T) {
		if _, err := (Config{TokenEnv: "HTTPCLIENT_TEST_TOKEN_UNSET"}).Token(); err == nil {
			t.Fatal("expected an error for unset env var")
		}
	})
}
