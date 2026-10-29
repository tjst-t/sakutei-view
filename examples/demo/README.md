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
- `redfish/` -- an optional `--redfish-config` and intent fragment for the
  `redfish-system` provider, targeting `server01`'s BMC.
- `run-demo.sh` -- starts the real Sakutei, the example plugin, and
  sakutei-view together in one step (containerlab-independent half of the
  demo; pass `REDFISH_CONFIG` once the topology below is deployed to also
  include the BMC piece).

## What's verified, and how

Everything in this directory has now been exercised for real, in this
repository's own development, using the actual public releases of
containerlab, Sakutei (`github.com/tjst-t/sakutei` v2.6.0, the real binary,
not a mock), and `ghcr.io/tjst-t/qemu-bmc` (the real BMC image) -- no part
of it is reference-only anymore:

1. **The containerlab topology deploys as written.** `containerlab deploy
   -t topology.clab.yml` brought up all three nodes (`server01`, `dns1`,
   `fw1`); `server01` reached Docker health state `healthy` with its
   Redfish server listening, and `containerlab destroy` tore the lab down
   cleanly afterward.

2. **`sakutei/intent/resources.yaml` parses and converges cleanly from an
   empty starting state**, confirmed with the real Sakutei binary:

   ```
   sakutei plan      -intent sakutei/intent -zone zone.db -ruleset ruleset.conf
   sakutei reconcile -intent sakutei/intent -zone zone.db -ruleset ruleset.conf
   ```

   produces 3 `CREATE` operations (two `dnszone` records, one `nftables`
   chain), and a second `plan` afterward reports `Converged (no changes)`.
   `zone.db` and `ruleset.conf` are not committed here -- Sakutei creates
   both from nothing on the first `reconcile` (a missing file reads as an
   empty zone/ruleset), and running the two commands above regenerates them
   identically for anyone who tries this.

3. **The `redfish-system` resource converges against the real BMC.** With
   the topology deployed and `redfish/bmcs.yaml` + a `secrets.json` copied
   from `redfish/secrets.json.example`, running Sakutei with `-intent`
   pointed at a directory containing both `sakutei/intent/resources.yaml`
   and `redfish/resources.yaml`, plus `-redfish-config redfish/bmcs.yaml`:
   `plan` showed `redfish-system//server01` needing `power: "Off" → "On"`;
   `reconcile` applied it (1 more change, 4 total with the dnszone/nftables
   resources above); a follow-up `plan` reported `Converged (no changes)`;
   and querying the qemu-bmc's Redfish endpoint directly
   (`GET /redfish/v1/Systems/1`) confirmed `PowerState: "On"` and
   `BootSourceOverrideEnabled: "Disabled"`, matching the intent. This is
   also what caught and drove the fix for the `local_id`-vs-`id` bug
   recorded in Backlog item `sakutei-graph-waves-sakutei-id-ready-fal`.

4. **The full demo-operation loop was watched live in sakutei-view,
   running against this real stack** (real Sakutei + real deployed
   containerlab lab + real qemu-bmc, all up together, launched with
   `run-demo.sh REDFISH_CONFIG=redfish/bmcs.yaml`): with
   `sakutei_graph.waves` showing all four resources `ready`, clicking
   **Disturb** on the `lun-drift` script in the Operations panel emitted a
   `Disturbed`/`firing` event in Live events in real time; clicking **Fix**
   emitted the matching `Fixed`/`resolved` event. (`lun-drift` is the
   example plugin's own self-contained demo resource, independent of the
   Sakutei-managed resources above -- it exercises the disturb/fix/SSE
   mechanics without needing Sakutei to manage drift on `server01` itself.)

   A real bug was found and fixed along the way: the Go binary's embedded
   frontend (`internal/webui/dist`, built once via `go:embed`) had gone
   stale after the Operations panel was added in a later change and was
   never rebuilt, so the single binary was silently missing the Operations
   panel entirely. `scripts/build-frontend.sh` was re-run and the refreshed
   `internal/webui/dist` committed; `go build ./cmd/sakutei-view` on a
   fresh checkout now ships the current frontend again.

What's still a real limitation, not a shortcut taken here: containerlab's
own `clab-api-server` (the generic `CONTAINERLAB_SOURCE=clab-api` backend)
could not be started in this particular development environment for an
unrelated reason (its container needs host networking / a privileged
container, which this environment's own execution-safety guard blocks
regardless of containerlab itself working fine). The verification above
therefore used `CONTAINERLAB_SOURCE=plugin` throughout, so the topology
diagram did not show the real containerlab-deployed nodes by name. Nothing
about this is specific to `clab-api-server`'s code; it's about what one
particular CI-like environment allows a container to request, and does not
reflect on `CONTAINERLAB_SOURCE=clab-api` itself.

## Running it for real

Quick start (containerlab-independent half -- real Sakutei, example
plugin, and sakutei-view, no BMC):

```sh
examples/demo/run-demo.sh
```

then open the printed URL and use the Operations panel to run the
`lun-drift` script's Disturb / Fix actions.

To also bring up the containerlab topology and the `redfish-system`
resource against the real BMC, on a machine with containerlab, KVM
(`/dev/kvm`) and `/dev/net/tun` available:

```sh
containerlab deploy -t examples/demo/topology.clab.yml

cp examples/demo/redfish/secrets.json.example examples/demo/redfish/secrets.json
# edit secrets.json with a real value if you're not using the example password

REDFISH_CONFIG="$(pwd)/examples/demo/redfish/bmcs.yaml" examples/demo/run-demo.sh
```

`run-demo.sh` merges `redfish/resources.yaml` into the intent it hands
Sakutei automatically whenever `REDFISH_CONFIG` is set. When you're done:

```sh
examples/demo/run-demo.sh stop
containerlab destroy -t examples/demo/topology.clab.yml --cleanup
```
