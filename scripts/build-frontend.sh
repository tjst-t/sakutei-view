#!/usr/bin/env bash
# build-frontend.sh builds the web/ frontend (Vite+React+TypeScript) and
# copies its production output into internal/webui/dist, which
# internal/webui embeds into the sakutei-view binary via go:embed. It also
# writes internal/webui/source-hash.txt, a hash of the web/ source dist/ was
# just built from -- internal/webui/sourcehash_test.go recomputes that hash
# under plain `go test` (no Node.js) and fails if web/ has since changed
# without dist/ being rebuilt, catching a stale embedded frontend early.
#
# Run this after changing anything under web/src (or web's dependencies),
# then commit the resulting internal/webui/dist and source-hash.txt
# alongside your change — both are checked into git (despite being
# generated output) specifically so that `go build ./cmd/sakutei-view`
# works on a fresh checkout with no Node.js involved. CI / local Go-only
# workflows never need to run this script; only frontend changes do.
#
# Usage: scripts/build-frontend.sh
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
repo_root="$(pwd)"

echo "==> npm ci (web/)"
(cd web && npm ci)

echo "==> npm run build (web/)"
(cd web && npm run build)

dest="$repo_root/internal/webui/dist"
echo "==> refreshing $dest"
rm -rf "$dest"
mkdir -p "$dest"
cp -r "$repo_root/web/dist/." "$dest/"

echo "==> writing internal/webui/source-hash.txt (staleness check for dist/)"
(cd "$repo_root" && go run ./scripts/gensourcehash)

echo "==> done. internal/webui/dist and source-hash.txt now match web/ — review with 'git status' and commit them together."
