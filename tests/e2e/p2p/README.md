# P2P end-to-end tests

Tests for the P2P layer (`core/services/p2pnet`, `core/services/p2prelay`,
`core/services/p2ppair`) that need more than a unit test:

| Path | What it is |
|---|---|
| `lab/` | A NAT lab in Docker: two peers behind NAT routers, a public segment with the real relay. The shell harness and the cases. |
| `labbin/` | The small command that runs the peers in the lab: a server, a dialer, an invite redeemer, a relay admin client. It is built from the real P2P packages. |

The relay in the lab is the real `localai-relay` binary (`cmd/localai-relay`),
not a test double. The peers use the real host (`p2pnet`), the real server side
of the relay protocol (`p2prelaymgr`) and the real invite client
(`p2ppair/pairclient`).

The unit tests of the packages sit next to the code and run with `make test-p2p`.

## Run it

You need Docker and a Linux kernel that does NAT in containers (any recent
Docker host does; Docker Desktop is fine too). No root is needed beyond the
Docker socket.

```sh
make test-p2p          # unit tests, race detector
make test-e2e-p2p      # builds the relay and the lab binary, runs every lab case
```

Run some cases only, keep the lab up to look around, and clean up:

```sh
make test-e2e-p2p P2P_LAB_ARGS="relay_restart open_mode"
LAB_KEEP=1 make test-e2e-p2p P2P_LAB_ARGS=unpaired_refused
tests/e2e/p2p/lab/lab.sh x a sh         # a shell on peer "a"
tests/e2e/p2p/lab/lab.sh down           # remove containers, networks, volume
tests/e2e/p2p/lab/lab.sh rmi            # remove the lab image
tests/e2e/p2p/lab/run.sh --list         # the cases
```

Settings, all optional: `P2P_LAB_BIN` and `P2P_LAB_RELAY_BIN` (use binaries
you built instead of `labbin` and `localai-relay`; they must be static, so build
with `CGO_ENABLED=0 GOOS=linux`), `P2P_LAB_LOGS` (log directory, default
`tests/e2e/p2p/lab/logs`), `LAB_NATS` (the two NAT types the cases start with,
default `sym sym`; a case may change them), `LAB_IPTABLES` (`iptables-legacy` if
the host kernel has no nftables).

The lab starts about ten small containers and needs less than 500 MB of
memory. The first run builds a small image (`p2plab-lab:local`, 20 MB); remove
it with `lab.sh rmi`.

## Read the logs

Everything goes to `tests/e2e/p2p/lab/logs/<case>/`:

| File | Content |
|---|---|
| `case.log` | The assertions of the case: `ok` or `FAIL` with what was expected and what was seen. Start here. |
| `a-*.log`, `b-*.log` | Output of every process on peer a or b: `<name>.out.log` is stdout, `<name>.err.log` is the log. `SRV`, `SRV2` are servers, `relayN` is the relay process. |
| `relay-relayN.*.log` | The relay, one pair of files per start (a restart makes `relay2`). |
| other `.log` | Output of single commands the case kept (dial results, admin calls). |

A few things to know when a case fails:

- Lines starting with `RESULT`, `TEST`, `PATH` and `CHECK` are the dialer's
  results. `PATH relayed(limited)` means the traffic went through the relay.
- The relay logs every decision: `DENY connection ... (not paired)`,
  `DENY circuit ... (no common admin)`, `allow reserve <peer>`,
  `revoked <peer>: closed N connection(s)`.
- `LAB_KEEP=1` leaves the containers up. Peer IDs of a run are in `logs/ids.log`.
  Container names start with `p2plab-`.
- CI uploads the same directory as the artifact `p2p-lab-logs` when the lab
  fails.

## CI

`.github/workflows/tests-e2e-p2p.yml` runs both targets on `ubuntu-latest`: on
pull requests and pushes that touch the P2P packages, the lab or the workflow,
and nightly. It needs no secrets. See the comments at the top of the workflow
for why GitHub-hosted runners can run the lab.

The lab itself, its topology and the contract of the lab binary are in
[`lab/README.md`](lab/README.md).
