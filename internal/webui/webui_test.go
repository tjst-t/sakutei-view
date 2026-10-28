package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFS_ServesIndexHTML(t *testing.T) {
	data, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html): %v", err)
	}
	if !strings.Contains(string(data), "<div id=\"root\">") {
		t.Errorf("index.html does not look like the built frontend's entry point: %s", data)
	}
}

func TestFS_ViaHTTPFileServer(t *testing.T) {
	srv := httptest.NewServer(http.FileServerFS(FS()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}
