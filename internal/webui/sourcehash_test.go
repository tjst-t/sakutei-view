package webui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSourceHashMatchesCommittedDist guards against internal/webui/dist
// silently going stale. scripts/build-frontend.sh writes source-hash.txt
// alongside dist/ every time it rebuilds the frontend, computed from the
// exact same web/ source dist was built from. If someone changes web/src
// (or any other git-tracked file under web/) without re-running that
// script and committing the refreshed dist/ + source-hash.txt, this test
// fails -- without needing Node.js/npm, since it only recomputes a hash
// over the already-checked-out web/ source and compares it to what is
// committed.
//
// This does not literally rebuild the frontend and diff dist/ byte-for-
// byte; it is a lighter-weight proxy that holds exactly as long as
// source-hash.txt and dist/ are always regenerated and committed together
// (which scripts/build-frontend.sh ensures -- there is no other way to
// produce a valid source-hash.txt).
func TestSourceHashMatchesCommittedDist(t *testing.T) {
	webDir := filepath.Join("..", "..", "web")
	if _, err := os.Stat(webDir); err != nil {
		t.Fatalf("web/ not found at %s (expected to run from internal/webui): %v", webDir, err)
	}

	got, err := ComputeSourceHash(webDir)
	if err != nil {
		t.Fatalf("ComputeSourceHash: %v", err)
	}

	wantBytes, err := os.ReadFile("source-hash.txt")
	if err != nil {
		t.Fatalf("reading source-hash.txt: %v (run scripts/build-frontend.sh)", err)
	}
	want := strings.TrimSpace(string(wantBytes))

	if got != want {
		t.Errorf("web/ source has changed since internal/webui/dist was last built.\n"+
			"committed source-hash.txt: %s\n"+
			"current web/ source hash:  %s\n"+
			"Run scripts/build-frontend.sh and commit the refreshed dist/ and source-hash.txt alongside your change.",
			want, got)
	}
}
