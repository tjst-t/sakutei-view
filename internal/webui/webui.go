// Package webui embeds the built frontend (web/) into the sakutei-view
// binary, so a deployment is a single executable with no Node.js involved.
//
// dist/ is NOT generated at build time — go:embed can only reference files
// that already exist on disk when the package is compiled. Instead, dist/
// is produced ahead of time by scripts/build-frontend.sh (which runs
// `npm run build` in web/ and copies web/dist's output here) and committed
// to git. Anyone changing web/src must re-run that script and commit the
// refreshed dist/ alongside their change; `go build ./cmd/sakutei-view`
// itself never invokes npm and does not need Node.js installed.
//
// Because dist/ is committed separately from the web/ source it was built
// from, it can silently go stale (someone edits web/src but forgets to
// re-run the build script and commit the refreshed dist/). source-hash.txt
// plus TestSourceHashMatchesCommittedDist (sourcehash_test.go) guard
// against that: build-frontend.sh writes a hash of the current web/ source
// to source-hash.txt every time it rebuilds dist/, and the test recomputes
// that same hash from the checked-out web/ source under plain `go test`
// (no Node.js needed) and fails if it no longer matches -- i.e. if web/ has
// changed since dist/ was last built.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the embedded frontend's build output (an index.html plus its
// assets) rooted at dist's own contents, suitable for http.FileServerFS.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Only possible if "dist" were missing from the embed.FS entirely,
		// which would mean scripts/build-frontend.sh was never run before
		// this package was compiled — a build-time setup error, not a
		// runtime one.
		panic("internal/webui: embedded dist/ is missing: " + err.Error())
	}
	return sub
}
