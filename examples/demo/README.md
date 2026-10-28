# Public demo configuration

A small, generic configuration for trying sakutei-view end to end: a
containerlab topology, a Sakutei intent, and sakutei-view's example plugin,
wired together so you can watch "something drifts → Sakutei fixes it" live
in the UI via `/api/events`.

Nothing here refers to any real deployment -- node names, addresses, and
resource IDs are all placeholders chosen for this demo.

## What's in here

- `topology.clab.yml` -- a small containerlab topology: one BMC-equipped
  server (`server01`, using `ghcr.io/tjst-t/qemu-bmc`) plus two plain hosts
  (`dns1`, `fw1`) standing in for a DNS server and a firewall.
- `sakutei/intent/resources.yaml` -- a Sakutei intent with two `dnszone` A
  records (for `dns1`) and one `nftables` chain (for `fw1`). Both providers
  are file-based (a local zone file and a local nftables ruleset file) and
  need no special privilege to run.
- `redfish/` -- an optional, reference-only `--redfish-config` and intent
  fragment for the `redfish-system` provider, targeting `server01`'s BMC.

## What's verified, and how

This environment cannot run containerlab (no privileged container runtime /
network namespaces available here), so the topology above has never itself
been deployed, and the `redfish/` BMC config has never itself been used
against a real BMC. Those two pieces are reference-only: they're written to
match Sakutei's and containerlab's real, documented schemas (cross-checked
against Sakutei's own upstream `docs/providers/redfish.md` and its own
integration-test topology, tag v2.6.0), but **running them has not been
confirmed in this repository's own development.**

What *has* been verified for real, in this repository's own development,
using the actual public release of Sakutei (`github.com/tjst-t/sakutei`,
the real binary, not a mock) and sakutei-view's own `examples/example-plugin`
(no containerlab):

1. `sakutei/intent/resources.yaml` parses and converges cleanly from an
   empty starting state:

   ```
   sakutei plan      --intent sakutei/intent --zone zone.db --ruleset ruleset.conf
   sakutei reconcile --intent sakutei/intent --zone zone.db --ruleset ruleset.conf
   ```

   produces 3 `CREATE` operations (two `dnszone` records, one `nftables`
   chain), and a second `plan` afterward reports `Converged (no changes)`.
   `zone.db` and `ruleset.conf` are not committed here -- Sakutei creates
   both from nothing on the first `reconcile` (a missing file reads as an
   empty zone/ruleset), and running the two commands above regenerates them
   identically for anyone who tries this.

2. Running the real Sakutei daemon against this intent
   (`sakutei --intent sakutei/intent --zone zone.db --ruleset ruleset.conf
   --read-listen 127.0.0.1:9472 ...`) alongside `examples/example-plugin`
   and sakutei-view itself (`CONTAINERLAB_SOURCE=plugin`), `GET
   /api/snapshot` correctly reports `sakutei_graph.waves` for these three
   resources (this is also what caught and drove the fix for the
   `local_id`-vs-`id` bug recorded in Backlog item
   `sakutei-graph-waves-sakutei-id-ready-fal`).

3. The full demo-operation loop against this stack -- `POST
   /api/actions/lun-drift/disturb` (example-plugin's built-in demo
   resource) turning the node amber in the topology view and emitting a
   `Disturbed`/`firing` pair on `/api/events`, then `POST
   /api/actions/lun-drift/fix` turning it back to green with a
   `Fixed`/`resolved` pair -- was exercised live with a real `curl -N`
   subscription to `/api/events` confirming both pairs arrive in the
   merged timeline, in real time.

In short: **the Sakutei intent and the product's disturb/fix/event loop are
verified against the real Sakutei release; the containerlab topology and
the BMC/redfish piece are not run or verified here, only written to match
the real documented schemas.**

## Running it for real

On a machine with containerlab, KVM (`/dev/kvm`) and `/dev/net/tun`
available:

```sh
containerlab deploy -t examples/demo/topology.clab.yml

cd examples/demo
sakutei reconcile --intent sakutei/intent --zone zone.db --ruleset ruleset.conf
sakutei --intent sakutei/intent --zone zone.db --ruleset ruleset.conf \
  --read-listen 127.0.0.1:9472 --interval 5s &

go run ./examples/example-plugin &   # from the repository root, in another shell

CONTAINERLAB_SOURCE=plugin \
SAKUTEI_BASE_URL=http://127.0.0.1:9472 \
PLUGIN_BASE_URL=http://127.0.0.1:8090 \
BASIC_AUTH_USER=demo BASIC_AUTH_PASSWORD=demo \
go run ./cmd/sakutei-view
```

then open sakutei-view in a browser and use the Operations panel to run
the `lun-drift` script's `disturb` / `fix` actions.

To also try the optional `redfish-system` resource: merge
`redfish/resources.yaml` into `sakutei/intent/`, and pass
`--redfish-config examples/demo/redfish/bmcs.yaml` to every `sakutei`
invocation above. Fill in `redfish/secrets.json.example` with a real
secret value and point `secret_store:` at it (copy it to `secrets.json`
first -- `secrets.json` itself is gitignored so a real value never ends up
committed).
