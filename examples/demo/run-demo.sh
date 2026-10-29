#!/usr/bin/env bash
# run-demo.sh -- bring up the sakutei-view demo stack in one step: the real
# Sakutei (fetched and built from the public release, not a mock), the
# example plugin, and sakutei-view itself, wired together against this
# directory's intent (examples/demo/sakutei/intent).
#
# This script does NOT touch containerlab or the redfish/ BMC piece -- it
# is the containerlab-independent half of the demo (see README.md for the
# full picture, including what still needs containerlab + a real BMC).
# To also exercise the redfish-system resource against the demo's
# containerlab topology, deploy examples/demo/topology.clab.yml first and
# then set REDFISH_CONFIG (see below) before running this script.
#
# Usage:
#   examples/demo/run-demo.sh          # start everything, foreground, Ctrl+C to stop
#   examples/demo/run-demo.sh stop     # stop a previously started (backgrounded) run
#
# Requires: go (1.22+); network access the first time, to fetch the real
# Sakutei module. Everything this script writes goes under
# examples/demo/.run/ and is never committed (see .gitignore) -- delete
# that directory to start over from nothing.
#
# Environment variables (all optional):
#   SAKUTEI_VERSION      tag of github.com/tjst-t/sakutei to build (default v2.6.0)
#   REDFISH_CONFIG        path to a --redfish-config file (e.g. redfish/bmcs.yaml)
#                          to also manage the redfish-system resource in
#                          redfish/resources.yaml -- requires a real BMC
#                          reachable at the address in that file (the demo's
#                          containerlab topology provides one; see README.md)
#   PLUGIN_PORT           port for the example plugin (default 8081)
#   SAKUTEI_READ_LISTEN   address for Sakutei's read-only API (default 127.0.0.1:9471)
#   VIEW_LISTEN           address for sakutei-view itself (default 127.0.0.1:8080)
#   BASIC_AUTH_USER / BASIC_AUTH_PASSWORD   sakutei-view's Basic auth (default demo/demo)
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
demo_dir="$(pwd)"
repo_root="$(cd ../.. && pwd)"
run_dir="$demo_dir/.run"
mkdir -p "$run_dir"

sakutei_version="${SAKUTEI_VERSION:-v2.6.0}"
sakutei_bin="$run_dir/sakutei"
plugin_port="${PLUGIN_PORT:-8081}"
sakutei_read_listen="${SAKUTEI_READ_LISTEN:-127.0.0.1:9471}"
view_listen="${VIEW_LISTEN:-127.0.0.1:8080}"
basic_auth_user="${BASIC_AUTH_USER:-demo}"
basic_auth_password="${BASIC_AUTH_PASSWORD:-demo}"

pidfile() { echo "$run_dir/$1.pid"; }

stop_all() {
  for name in sakutei-view plugin sakutei; do
    f="$(pidfile "$name")"
    if [ -f "$f" ]; then
      pid="$(cat "$f" 2>/dev/null || true)"
      if [ -n "${pid:-}" ] && kill -0 "$pid" 2>/dev/null; then
        echo "stopping $name (pid $pid)"
        kill "$pid" 2>/dev/null || true
      fi
      rm -f "$f"
    fi
  done
}

if [ "${1:-}" = "stop" ]; then
  stop_all
  exit 0
fi

trap 'echo; echo "==> stopping"; stop_all' EXIT INT TERM

intent_dir="$demo_dir/sakutei/intent"
redfish_args=()
if [ -n "${REDFISH_CONFIG:-}" ]; then
  echo "==> REDFISH_CONFIG set: including the redfish-system resource"
  intent_dir="$run_dir/intent"
  rm -rf "$intent_dir"
  mkdir -p "$intent_dir"
  cp "$demo_dir/sakutei/intent/resources.yaml" "$intent_dir/"
  cp "$demo_dir/redfish/resources.yaml" "$intent_dir/redfish-resources.yaml"
  redfish_args=(-redfish-config "$REDFISH_CONFIG")
fi

echo "==> building sakutei-view"
( cd "$repo_root" && go build -o "$run_dir/sakutei-view" ./cmd/sakutei-view )

echo "==> building the example plugin"
( cd "$repo_root" && go build -o "$run_dir/example-plugin" ./examples/example-plugin )

if [ ! -x "$sakutei_bin" ]; then
  echo "==> fetching + building the real Sakutei (${sakutei_version})"
  GOBIN="$run_dir" go install "github.com/tjst-t/sakutei/v2/cmd/sakutei@${sakutei_version}"
fi

echo "==> starting the example plugin (127.0.0.1:${plugin_port})"
LISTEN_ADDR="127.0.0.1:${plugin_port}" "$run_dir/example-plugin" \
  >"$run_dir/plugin.log" 2>&1 &
echo $! >"$(pidfile plugin)"

echo "==> starting Sakutei (${sakutei_read_listen}), intent=${intent_dir}"
"$sakutei_bin" \
  -intent "$intent_dir" \
  -zone "$run_dir/zone.db" \
  -ruleset "$run_dir/ruleset.conf" \
  -read-listen "$sakutei_read_listen" \
  -interval 3s \
  "${redfish_args[@]}" \
  >"$run_dir/sakutei.log" 2>&1 &
echo $! >"$(pidfile sakutei)"

sleep 2

echo "==> starting sakutei-view (${view_listen})"
CONTAINERLAB_SOURCE=plugin \
PLUGIN_BASE_URL="http://127.0.0.1:${plugin_port}" \
SAKUTEI_BASE_URL="http://${sakutei_read_listen}" \
BASIC_AUTH_USER="$basic_auth_user" \
BASIC_AUTH_PASSWORD="$basic_auth_password" \
LISTEN_ADDR="$view_listen" \
"$run_dir/sakutei-view" >"$run_dir/sakutei-view.log" 2>&1 &
echo $! >"$(pidfile sakutei-view)"

sleep 1

cat <<BANNER

==================================================================
sakutei-view is up: http://${view_listen}/
  user: ${basic_auth_user}   password: ${basic_auth_password}

Open it in a browser, then use the Operations panel to run the
"lun-drift" script's Disturb / Fix actions and watch them show up
in Live events.

Logs: ${run_dir}/{sakutei,plugin,sakutei-view}.log
Press Ctrl+C to stop everything (or run this script again with "stop").
==================================================================

BANNER

wait
