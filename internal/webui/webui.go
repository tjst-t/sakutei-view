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
