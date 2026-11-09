+++
title = "Testing add-ons"
toc = true
description = "Prepare and verify the toolchain for focused addon tests."
weight = 50
aliases = ["/development/addon-testing/"]
+++

The addon verification scripts separate explicit dependency setup from offline checks.
Run all commands from the repository root. `scripts/addons/suites.json` lists the available suites and their exact case inventories.
For typed extensions, run `contracts`, `apps`, `runtime`, `gateway`, and `host-slots`.
The `contracts` suite includes all official package manifests and generic architecture guards. It runs `./pkg/addons/contract`,
`./pkg/addons/packaging`, and the root architecture suite, each with an exact
Ginkgo report inventory (including twelve official manifest audits, two policy
packaging checks, and six architecture guards). The OAuth guard parses the
files of the OAuth logins (the contract descriptor, the token client, the
connector service and stores, the gateway, the routes and the application
wiring), their React sources (the Connectors page included) and their locale
strings. It finds the files by name patterns, so a new OAuth file next to the
others is checked without a change to the guard, and a pattern that matches
no file fails it, so a move is noticed. It fails on a string that names an identity or cloud
service or one of its hosts, from a deny list kept in the test; on an authorize
parameter in the contract registry that is not on the reviewed list of the
test; and on an import of an OAuth library or a service SDK. A second case
feeds it synthetic files to prove each check fails. `apps` inventories the generic
UI fixtures; `gateway` inventories the real application owner binding fixture.
The latter installs two differently named packages through the actual catalog
and Manager, uses JSON chat/agent stores, selects the oldest provider, then pins
the second and checks each declared execution/status tool and runtime identity.
The gateway callback-only test separately checks honest binding pass-through.

The shared `scripts/addons/ginkgo_report.py` comparator checks actual report
artifacts, not just JSON syntax. Every required case must pass exactly once.
A report may declare an explicit `focus` matching its command's Ginkgo focus;
only excluded specs may be skipped, never required cases. Harness self-tests
exercise missing artifacts, missing required cases, and skipped reports.
Unknown suites, empty selections, skipped required tests, and failed commands return a nonzero status.

## Direct verification evidence and limits

The bounded review verification used offline caches, `GOMAXPROCS=2`, and
`GOFLAGS='-mod=readonly -p=2 "-ldflags=-s -w"'`. Direct commands used
`go test -timeout=5m -count=1 -tags auth PACKAGE -ginkgo.json-report=REPORT`
for `./pkg/addons/contract`, `./pkg/addons/packaging`, `.`,
`./core/services/addons/apps`,
`./core/services/addons/gateway`, and `./core/application`.
Actual reports compared with the harness comparator contain 196 contract,
174 packaging, 2 architecture, 10 apps, and 115 gateway cases, all passed.
The focused `execution binding owner resolver` run passed all 8 required cases;
its excluded specs are not required cases. Full application testing exited zero
with 256 passed and 3 PostgreSQL-dependent Files cases skipped because
`LOCALAI_TEST_POSTGRES_URL` was unset; it is not an all-cases acceptance pass.

`sh scripts/addons/prerequisites_test.sh` passed its exact 38-case inventory,
including missing/skipped report rejection. Model race testing and viewer tests
passed, and `go run ./pkg/addons/model/executionstatus/cmd/viewer -check`
verified trace coverage and deterministic data. Focused real owner artifacts
with a removed or skipped required case were also rejected by the comparator.
Existing contract coverage passed without an invented failing baseline.

These are direct checks, **not a full harness pass**. The commands and report
inventories are wired, but `sh scripts/addons/prepare.sh --check` remains blocked
by missing `protoc` 26.1. No downloads or integration preparation were performed.
Browser acceptance and named-user SQL authority integration remain unverified;
the JSON owner fixture does not establish either. Local evidence reports and
logs are under `.cache/addons/logs/review2-*` and are not product artifacts.

## Required tools

Provide these tools before running setup:

- Go 1.26.x, with `GOTOOLCHAIN=local`. Other minor versions are rejected.
- Node 22.12 or later within Node 22, with npm. The validated setup uses Node 22.22.0.
- `protoc` 26.1. The general Makefile downloader uses a different version.
- A native C compiler (`cc`, or the executable specified by `CC`) with `CGO_ENABLED=1`.
- Python 3, POSIX shell tools, and the runtime libraries required by Chromium.

Setup and integration preparation require at least 20 GiB of free disk space before each major step.
They do not install system packages or fetch Go, Node, or `protoc` automatically.
Place locally provisioned executables in `.cache/addons/bin`, or put them on `PATH`.

For Linux x86-64, the validated tool archives have these SHA-256 checksums:

| Archive | Source | SHA-256 |
| --- | --- | --- |
| `node-v22.22.0-linux-x64.tar.xz` | [Node 22.22.0 distribution](https://nodejs.org/dist/v22.22.0/) | `9aa8e9d2298ab68c600bd6fb86a6c13bce11a4eca1ba9b39d79fa021755d7c37` |
| `protoc-26.1-linux-x86_64.zip` | [Protocol Buffers 26.1 release](https://github.com/protocolbuffers/protobuf/releases/tag/v26.1) | `a7be2928c0454f132c599e25b79b7ad1b57663f2337d7f7e468a1d59b98ec1b0` |

Validate archive checksums before extraction. These archives do not apply to other architectures.

## Explicit setup

Setup downloads Go dependencies, installs the pinned protobuf plugins, and runs `npm ci` against the React lockfile.
It then installs Chromium through the local Playwright CLI, unless you supply an existing browser.

```sh
sh scripts/addons/prepare.sh --prepare
```

The protobuf plugin pins match the Makefile:

- `google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2`
- `google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af`

Go resolves the second pin to module version `v1.4.0`.
The check validates each plugin's embedded module version and checksum, not its version label alone.
Go downloads use module checksum authentication. npm uses the lockfile's package integrity records.

The lockfile currently selects Playwright 1.58.2 and Chromium revision 1208 (Chrome 145.0.7632.6).
The installed Playwright package selects the browser revision; the scripts never invoke `npx`.
To provision only the browser after npm setup, run:

```sh
export PATH="$PWD/.cache/addons/bin:$PATH"
export PLAYWRIGHT_BROWSERS_PATH="$PWD/.cache/addons/browsers"
core/http/react-ui/node_modules/.bin/playwright install chromium
```

Keep `PLAYWRIGHT_BROWSERS_PATH` set to this worktree-local path for manual installation.
The preparation and verification scripts set it automatically.
A browser installed in Playwright's default user cache does not satisfy this setup.

Alternatively, set `PLAYWRIGHT_CHROMIUM_PATH` to an absolute executable path before setup and verification.
Setup then skips the browser download. Verification still requires a successful browser launch and rendered UI.
Neither option installs OS libraries. If launch fails, inspect the reported library names and the browser executable with `ldd` on Linux.
Provide a compatible runtime environment or report the missing libraries as a blocker; do not skip the browser probe.

## Prepare generated inputs

After explicit setup, generate only the inputs needed by the focused tests:

```sh
sh scripts/addons/prepare.sh --integration
```

This command generates `pkg/grpc/proto`, builds the React distribution, and builds two auth-enabled test binaries:

- `tests/e2e/mock-backend/mock-backend`
- `tests/e2e-ui/ui-test-server`

It does not build inference backends or run a full repository build.
It disables Go dependency downloads and requires the populated setup cache.
Repeat this step when the proto, UI, or test-server sources change.

## Check and verify

Inspect the toolchain, installed dependencies, and generated inputs without downloading or rebuilding them:

```sh
sh scripts/addons/prepare.sh --check
```

This read-only dependency check is not a browser or SQLite runtime test.
Run the complete readiness gate separately:

```sh
sh scripts/addons/verify.sh prerequisites
```

Verification performs these checks:

1. Run isolated shell fixtures for missing tools, incompatible versions, failed children, invalid reports, and empty or skipped selections.
2. Inspect the real toolchain and generated inputs.
3. Compile and execute a SQLite transaction with CGO, Ginkgo v2, and Gomega.
4. Start the real UI test server with the mock backend on an isolated loopback port.
5. Wait for server health, launch Chromium, and require a rendered React root.

The SQLite probe uses `go test -count=1 -tags auth` and a machine-readable Ginkgo report.
Verification rejects missing, empty, failed, skipped, or unexpected test cases.
It records child commands, output, and status in `.cache/addons/logs/verify-*.jsonl`.
Ginkgo reports use separate unique files in the same directory.
Server processes stop after the probe, including when the browser check fails.

Checks and verification never install dependencies or browsers.
They set `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, and `GOFLAGS=-mod=readonly` for verification builds.
The npm dependency check uses `--offline`.
Missing cached modules or browser files are failures, not permission to download them.

## Cache layout and test inventory

The scripts keep caches and transient results under `.cache/addons`:

| Path | Purpose |
| --- | --- |
| `bin` | Local tools and protobuf plugins; prepended to `PATH` |
| `go`, `go/pkg/mod` | `GOPATH` and `GOMODCACHE` |
| `go-build` | `GOCACHE` |
| `npm` | npm cache |
| `browsers` | Playwright browser installations |
| `tmp` | Setup, fixture, and verification scratch data |
| `logs` | Command results and Ginkgo reports |
| `downloads` | Optional archives from explicit local provisioning |

Do not commit caches, generated distributions, or test binaries.
Keep the command and required-case inventory in `scripts/addons/suites.json` synchronized with the runner.
Add future suites only when their actual tests and commands exist.
A successful prerequisite gate demonstrates readiness, not acceptance of addon functionality that has no tests yet.

## Gateway verification

Run `sh scripts/addons/verify.sh gateway` for the owner carrier and real-auth HTTP suites.
The inventory lists each report separately and requires their exact combined case count.
Missing reports, skipped cases, empty reports, and unsuccessful child commands fail the gate.
The tests use local certificate authorities, pinned mTLS peers, two receiving frontends,
and a real SQLite identity database. No external identity service or addon port is exposed.

The disconnect fixture consumes the POST envelope before waiting for cancellation.
Leaving that body unread prevents the HTTP/1 server from observing disconnect while
its handler waits, which can strand test-server cleanup. Production owner handlers decode
the bounded envelope before starting a relay.

### Sharing across frontends

The policy package runs the owner's sharing policy behind the real carrier, and
two frontends forward to it (`gateway/carriertest`). The specs cover these cases.
A resource of a kind that the owner shares is listed from the second frontend.
A grantee's work on one frontend stops before a revoke through the other
frontend answers. A lowering decided on the owner stops the work it takes away.
The work that the new role still allows keeps running. A frontend that does not
stop in time gives `ErrDrainPending`. A lease that nobody renews expires. A lease
is honored only for the person who holds it. When the owner is gone, every call
gives the typed unavailable answer, and the running work stops. The chats
package runs the same rig with two chat services. The owner shares a saved agent
on frontend A, and the grantee lists it and chats with it on frontend B. A revoke
on A stops the turn on B before it answers. While the owner is gone, B lists and
runs no shared agent.

The application package runs a real add-on owner behind the same carrier, with two
frontends that hold no policy, for the connector bindings and the collections. A
name keeps one record from either frontend, and the policy specs forget it only
after the grantee's run stopped. The owner makes a collection shareable on A, and
the grantee lists, searches and writes it on B. A revoke on A stops a write on B
before it answers, and a lowering stops only the work the new role takes away. A
binding shared on A is listed on B with its name and owner and no credential, and a
revoke through B stops a call that uses the credential on the owner. While the
owner is gone, B stops the work on a shared collection and lists, shares and admits
nothing, and the person's own collections still work.

## OAuth conformance suite

```sh
sh scripts/addons/verify.sh oauth
```

The `oauth` suite (`tests/addons/oauth`) proves that the OAuth logins of
add-ons need no code for one service. It runs the real application: accounts
on a SQLite database, the add-on owner, the library, the supervisor, the
connectors and the HTTP routes. One test-only executable
(`addons/internal/oauthfixture`, built on the Go protocol package) is packaged
three times through the library, as `provider-a`, `provider-b` and
`provider-c`. The manifests differ in the ID, the title, the connector kind,
the authorize, token and revocation addresses, the host patterns, the scopes,
the authorize parameters and the client authentication (`basic`, `post` and
`none`). Each table of the suite runs the same steps for all three, so a host
that treated one service in a special way fails for the others.

The services are fakes from `core/services/studio/oauthtoken/oauthtest`: TLS
servers on loopback with an authorize, a token and a revocation endpoint. They
check PKCE, the redirect address and the client authentication the client was
registered with, and record each call. The host's guarded client refuses
loopback addresses, so the suite gives the connectors a token client whose
dialer reaches the fakes by host name, through
`application.UseConnectorOptionsForTests`. TLS is still verified, and the
descriptor checks of the host stay in place.

| Table | Proves |
| --- | --- |
| login | The administrator approves the digest; a login starts, the service redirects, the callback trades the code once and the binding shows the account. A tool call gets the access token and the granted scopes and no refresh token, client secret, code or verifier. When the token ends, twelve calls at once make one token call, and the rotated refresh token is stored. A `credential_rejected` answer gives one renewal and a retry; a second one gives `reconnect_needed` and the note for the model. `invalid_grant` gives `reconnect_needed`. A person who declines leaves the login as it was. Disconnect revokes at the service and deletes the login. Every call authenticates the client as the descriptor says, on the wire. No answer and no log line carries a secret. |
| failures | A login with fewer than the required scopes fails with `scopes_missing`; fewer optional scopes are kept as granted. A 5xx of the token endpoint is an outage and a 429 is `rate_limited` with its wait; neither ends the login. |
| short token | A token that lives one minute is used for half of its life before it is renewed. |
| upgrade | An upgrade that moves the token endpoint ends the approval. The next call gets `reconnect_needed` with the reason `needs_approval`, and no refresh token is sent anywhere. After a new approval the login uses the new address. |
| two frontends | A second application runs as a frontend on the same account database and carrier keys. A callback that reaches it is finished by the owner, tool calls through it are served and renewed on the owner, a state works once on either node, and a disconnect through it revokes. The frontend's token client reaches no service, so a token call made on the frontend would fail. |

The suite runs in seconds. It needs `pkg/grpc/proto` and the React
distribution, like the other suites that build the HTTP server.

## Data recovery suite

```sh
sh scripts/addons/verify.sh recovery
```

The `recovery` suite checks backup, upgrade with a migration, restore and purge
of add-on data. `scripts/addons/recovery.sh` runs, in this order: the node unit
tests of the panel rules, the typed client and the locale keys; the Go specs of
`core/services/addons/recovery` (every journal phase of an upgrade and of a
restore, with failures injected through test-only seams, cancellation, a
corrupt backup, too little storage, retention, grant resurrection and purge),
the specs of the policy hold and restore filter, the specs of the Tables format
migration, and `tests/addons/recovery`, which runs the real owner, real
accounts and the real Tables executable over HTTP; and a Playwright spec against
a UI test server. Set `RECOVERY_SHOTS` to a directory to keep the screenshots of
the Playwright run. The suite is part of the foundation gate. Run
`sh scripts/addons/prepare.sh --integration` first: the runner refuses a
server older than the recovery code or a UI build older than the panel.

## Foundation acceptance gate

The foundation gate is the end-to-end check of everything below the first
add-on: the owner, the carrier, the lifecycle, the policy, the proposals, the
file results, the Apps surface and the UI asset route. It is one integration
fixture, not a set of fakes at the seams.

```sh
sh scripts/addons/verify.sh foundation-acceptance   # the end-to-end suite
sh scripts/addons/foundation_gate.sh                 # every foundation suite
```

Run `sh scripts/addons/prepare.sh --integration` first. The gate builds the
server, the protocol fixture and the cluster command itself, so it never tests a
stale binary.

### The fixture

`tests/addons/foundation/cluster` starts three server processes of
`tests/e2e-ui`: the owner and two frontends (`front-a`, `front-b`). Each has its
own listener, data directory and carrier identity. They share one account
database, and they speak the real pinned mTLS carrier. The frontends open no
add-on storage and start no process. The add-on process is the protocol fixture
(`tests/addons/fixture/cmd`), packaged three ways from a catalog served on
loopback after the host was built:

| Package | Declares |
| --- | --- |
| `found-ui` | a service, the tools `echo` and `export`, and a UI module |
| `found-tool` | a service and the same tools, no UI |
| `found-svc` | a service only: no tool and no UI |

The fixture add-on reads `fixture.json` from its package. A mode changes how it
fails: `exit-start`, `never-ready`, `lose-reply` (commit, then drop the reply)
and `orphan` (ignore the closing of the startup channel). Its commits and every
apply request are written to a durable log, so a spec counts side effects across
restarts. Specs use real API keys and real browser sessions; the only route that
is not product code is the owner's loopback control route that registers a
resource, because the fixture has no add-on that creates one.

### What the gate proves

| Case group | Proves |
| --- | --- |
| install, activate, alternate | An install and an enable through a frontend reach the owner, start one process per add-on under the owner only, and reads alternate through the owner and both frontends |
| host unchanged | The host binary and the shipped UI build have the same digest after the whole lifecycle |
| actor propagation | The add-on sees the actor of the credential; forged actor and authority headers change nothing, and a forged administrator cannot reach lifecycle |
| JSON, progress, files, assets | JSON and file relays and lifecycle progress work through a non-owner frontend, a private file downloads through another frontend, and UI assets are served by digest, immutable and only for declared files |
| denials | A missing feature, a stranger, a stale generation and an undeclared tool give the same typed denial on every node |
| exactly once | A confirmed change reaches the add-on once, even confirmed again through another frontend; a lost reply is `outcome_unknown` with the original operation ID and is settled by receipt lookup with no second apply |
| owner down | Every frontend answers 503 with the same body, starts nothing and creates no storage; a restart brings back the last good package and the retained data |
| no duplicate owner | A second owner on the same storage does not keep running, and each add-on keeps one process |
| orphans | A process that outlived its owner is stopped before the next one starts |
| failed upgrade and enable | A new generation that cannot start leaves the last good one running; killing the owner in the middle of an upgrade or an enable keeps the last good code, the retained data and a usable lifecycle |
| disable, remove, reinstall | Data and grants outlive the removal, and a stale generation and its assets are denied after an upgrade |
| sessions and keys | Ending a session or revoking a key on one frontend refuses it on every node |
| browser | A member sees the add-on list only and the server refuses every management request; Open works through a non-owner frontend; a proxy path prefix works for deep links; a session ended on one frontend loses the assets on the others |
| service layer | An upgrade that cannot start keeps the last good generation running and the data; a request canceled during the start puts the last good generation back, keeps the operation open and the same operation ID finishes it; one broken add-on does not stop the owner |
| regression | The existing model and backend gallery specs (`core/gallery`), the gallery operation specs (`core/services/galleryop`) and the process adapter specs (`pkg/model`) pass |

The regression packages are the narrowest ones that run offline in under two
minutes each and that cover the code the add-on lifecycle sits beside. The
`core/gallery` model install specs run with `FIXTURES=tests/fixtures`. The only
skips the gate accepts are the specs that state they need macOS.

One `pkg/model` spec, "includes the exit code and final stderr line for an
unexpected exit", replaces the process-wide logger to capture a warning. It is
a test of the upstream package and the gate does not change it. Run together
with the other specs of the package, goroutines of those specs can log while
the logger is swapped and fail it at random. The gate therefore runs the
package three times: a dry run that fixes the inventory, a run that skips that
one spec, and a run that focuses on it alone. The case passes only when the
dry run lists exactly one such spec, the second run passes every other spec
(or skips it for macOS) and the third run passes that spec once. A spec that is
not accounted for in exactly one of the runs fails the case. A real failure of
the spec still fails the gate, with no retry.

### Reading the result

`foundation_gate.sh` runs every suite in the gate set, in order, and prints the
inventory: the passed count and the skipped count of each suite, and the total.
It exits nonzero when a suite fails, reports a skipped case, or is missing from
the set. A suite that is added to `suites.json` must be named in the script.
The report of the parent review is this inventory plus the case list of
`foundation-acceptance` in `scripts/addons/suites.json`.

## Library sources suite

`sh scripts/addons/verify.sh library-sources` runs the library sources spec
(`core/http/react-ui/e2e/library-sources.spec.js`) in a real browser, against a
real UI test server that starts like a normal install: the default galleries,
the default library sources of the add-on, backend and model kinds, and add-ons
on with nothing installed. `playwright.library.config.js` starts that server
itself on a free port. The spec adds, turns off, removes and restores sources,
checks that each page lists only its own kind, and checks the "Use official
libraries" setting.

## Continuous integration

Each part of the add-on tests has a job that runs it, so a pull request cannot
skip them:

| What | Where |
| --- | --- |
| Go tests under `addons/` and the self-contained `tests/addons/actione2e` suite (real Sandbox and Office executables under the real supervisor) | `make test` and the coverage run, job `tests-linux` in `test.yml` |
| Every suite of `suites.json` except `packaging`, in five groups | `addons-tests.yml` |
| `packaging` | `addons-package.yml` |
| UI unit tests of every add-on, the host UI unit tests, eslint and the locale ratchet | `tests-ui-unit.yml` (`make test-ui-unit`) |
| Add-on pages in the browser against stubbed routes, and the shipped bundle | `tests-ui-e2e.yml` |

The suites of `addons-tests.yml` need the integration build, so each group runs
`prepare.sh --prepare` and `--integration` once, then `verify.sh` for each of
its suites. Run the same way locally with
`make test-addons-suites ADDON_SUITES='gallery-ui library-sources'`.
A suite added to `suites.json` must be named in a group of the workflow and in
`foundation_gate.sh` or `tables_gate.sh`.

## Packaging suite

`sh scripts/addons/verify.sh packaging` checks the first-party OCI package, its release
channels and its workflow. It runs `scripts/addons/package_test.sh`, which prints one
JSON line for each case. It needs no registry, no Docker and no network.

- Go specs of `pkg/addons/packaging`. They build real layouts and then rewrite one part
  of them to prove each rule: missing entrypoint, missing declared UI, missing runtime
  library, manifest or platform mismatch, unexpected layers or media types, digest
  mismatch, invalid permissions, traversal paths, a bundled React runtime and a partial
  platform set. They cover the event and channel table (empty, Unicode and long refs,
  `latest`, `development`, semver-like names and two refs that normalize alike). They
  publish to an in-process registry and check immutable tags, digest-identical retries,
  stale promotions and a failed promotion that leaves aliases unchanged. They rewrite a
  branch so that the commit an alias names is gone from the checkout, and check that
  only a push to the channel's own ref takes it over, that `development` refuses, and
  that other git errors fail. The workflow policy has one mutated copy of the workflow
  for each rule, including the `validate` chain (`publish` must need `validate`,
  `validate` must not use `continue-on-error`, `always()` or `failure()`).
- `tests/addons/packaging`. It builds the Tables executable, runs `package.sh`, publishes
  to a loopback distribution, signs the platform image with the fixture authority under
  the identity of the rendered catalog entry, installs it with the real installer and
  starts the executable under the real supervisor. A signature of another workflow
  identity, an owner policy that names the backend workflow and a wrong pinned digest
  are each refused.
- Shell cases for `package.sh`, `channel.sh` and `addonpkg policy`, and static checks
  that the packaging scripts never push, tag, sign or log in and never name a backend
  registry.

The suite never signs, pushes or creates a tag against a real registry. The workflow
itself (registry login, keyless signing, the verification command) is covered by the
policy assertions and by review, not by a live run: running it needs the protected
environment of the repository.

## Tables acceptance

`sh scripts/addons/verify.sh tables-acceptance` is the end-to-end acceptance of the
first-party Tables add-on. `sh scripts/addons/tables_gate.sh` runs every `tables-*`
suite and then this one, in order, and prints the inventory like the foundation gate.
It fails for a suite that is skipped, that leaves a process behind or that is added to
`suites.json` and not named in the script.

What it runs, and what is real:

- **The cluster** is the foundation cluster of `tests/addons/foundation/cluster`: one
  owner and two forwarding frontends, each a separate server process with its own
  listener, one shared account database and the real pinned mTLS carrier. The accounts
  are real: an administrator, the owner of a database, an editor, a viewer, a stranger
  and an account without the add-ons feature, each with a stored session and an API key.
- **The package** is the real Tables. `scripts/addons/package.sh` builds the OCI layout
  of this platform (twice, for two versions). The suite checks every layer against its
  digest, joins the layers into the archive a catalog on loopback serves, and installs
  it through a frontend as an administrator does. Nothing is pushed, tagged or signed,
  and a receipt that says `publishable` is refused.
- **Calls** use the public HTTP routes only. There is no route into the add-on that a
  user could not use, and no seam in the add-on: crashes are `SIGKILL`, a stopped
  process is `SIGSTOP`, an owner outage is a killed owner process.
- **Reads of the database** by the test are read-only SQLite opens of the add-on's own
  file, to count the receipts it committed.

The specs, by what they prove:

| Area | Specs |
|---|---|
| Roles | Owner, editor, viewer, administrator, stranger and an account without the feature, over the browser module route, the REST routes, the typed proposal route and the chat tool consumer, each through a different node. Blocked consumers (`/v1/messages`, `/v1/responses`, `/v1/mcp/chat/completions`) fail with the typed `addon_tools_unsupported`. |
| Credentials | A paused key, a withdrawn feature, a lowered role (and the proposals it held), a revoked grant and a disabled account are denied on the next call on every node. |
| Authority | Forged actor and forwarding headers, authority, role and approval fields, and every way to confirm a proposal without the proposer's own tab (a key with the right headers, the owner's tab, an administrator, a missing header, a tool-call marker, a wrong digest). |
| Consistency | Schema and data staleness from two tabs, replay of an operation ID from every node, a conflicting request under the same ID, a burst from three nodes with exactly one receipt for each acknowledged ID, and a revocation that races a confirmation. |
| Crash | The add-on process killed in the middle of a batch and the owner killed and restarted in the middle of a batch: every acknowledged change survives, no change is applied twice, and a worker repeats only the same ID. A stopped and killed process leaves an unresolved confirmation that blocks other writes until the owner role resolves it. |
| Lifecycle | Disable, enable, uninstall and install: stale tabs and proposals of the older generation do nothing, and data and grants remain. |
| Files | CSV through a frontend, downloads from every node by the requester only, a revoked viewer, a disabled add-on. |
| Outage | With the owner down, a JSON read, the JSON relay, progress, status, file status, file download, an asset, the apps list, a proposal, the pending list, a confirmation, an apply and a chat tool call all fail closed (503 with the same typed answer from both frontends), and the frontends run nothing and create no storage. |
| Recovery | Backup, verify and restore by the administrator's tab; a corrupt backup, a running add-on, a wrong confirmation, an unknown backup. |
| Upgrade | An explicit check, a redacted status for a member, a key refused, a stale or changed pin refused, one pinned upgrade, the data and grants kept, a backup made first, and a replay of the same operation from another node. |
| Browser | Approval cards from chat on one frontend and the result on the other, prefix deep links, keyboard, 400 pixels, both themes, the unavailable page while the owner is down and the reload after it is back, and nothing left after logout. |

### The evidence manifest

Each run writes `.cache/addons/tables-acceptance-evidence.json`: the commands the run
executed, the case inventory with the state of every case, the native platform
(operating system and architecture), the digests of both packaged versions (the
archive the catalog serves and the image of the OCI layout), a statement that nothing
was published, and, for a case that failed, the path of the retained trace (the logs of
the three servers). A Go test (`TestEvidenceManifest`) checks it against the inventory
of `scripts/addons/suites.json`: the platform is native, the digests are well formed and
distinct, every case passed, and a failed case has a trace that exists.

### Faults the real executable cannot show on its own

Three cases need a fault that the product executable does not have, and the product
executable gets no flag or variable for them. The suite builds a second, test-only
executable from `addons/tables/cmd/tables-faults`, packages it with the same
`scripts/addons/package.sh` and installs it on one cluster. It serves exactly what the
product serves. A spec arms a fault by creating a file in the `fault` directory of the
add-on's data:

- `crash-after-commit` ends the process after the next change committed and before the
  answer is sent. The spec checks that the host reports the original operation ID as
  unknown from the frontend, that the receipt lookup finds it committed, and that the same
  operation again returns the same receipt: one row and one receipt in total.
- `hold-output` keeps a stream of the add-on from being served, so a transfer stays in
  flight. The spec disables the add-on and checks that the artifact ends without being
  downloadable on any node, that no byte stays on disk and that a new export works.

The expiry spec needs a short artifact lifetime. The fixture's cluster configuration has
an `artifactExpiryMillis` field, which the test launcher passes to the owner as the
programmatic `AddonConfig.ArtifactExpiry` option. No flag sets it and the default stays
one hour. The spec checks that every node gives the same denial for the expired file
and that the database and the backups are untouched.

### What it does not prove

- **Other platforms.** The evidence is of the platform it ran on. Advertising another
  platform needs a native run on that platform; the platform matrix keeps it disabled.
- **The free space seam** is a storage bound that needs a seam the real add-on does not
  have. The `recovery` suite covers it with an in-process seam. The expiry and the
  withdrawal of an artifact are covered here (see below).
- **A needs-recovery upgrade** needs a data format change that fails to migrate. The
  `recovery` suite covers it; this suite covers the successful upgrade and the restore.
- **Which interleaving a race takes.** The race and the crash specs accept whatever
  order happens and check the invariants. The committed-and-lost-reply case is not left
  to chance: see the fault specs below.
- **A refused request is a typed `400`.** A request that the add-on refuses as invalid
  (an unknown table, an unknown field) is `400` `invalid_request` on every route: the
  runtime client types the add-on's HTTP 400 and 422 as a refusal, and the host answers
  it the same way from the apply, read, proposal, chat and artifact paths, and from the
  owner and every frontend. The authorization specs pin the exact status.

### Settings authority in both topologies

For every add-on settings surface, run combined-mode and owner-with-two-frontends cases. Save through one frontend and verify the other frontend and the real consumer agree. Repeat a restrictive update, restart the owner, and interrupt owner access. Assert explicit errors rather than frontend-local settings or built-in fallback. Exercise CAS conflicts, per-key provider deletion, uncertain retries, and preservation of unrelated fields. Network policy tests must use new HTTP and CONNECT sockets after revocation; established streams may finish. Add rendered-form tests for the actual dispatch payload and disabled controls during failed reconciliation. Follow the existing distributed-state checklist before introducing a new settings path.

## Document skill migration checks

Run the contract, package, and packaging gates separately:

```sh
sh scripts/addons/verify.sh contracts
sh scripts/addons/verify.sh packages
sh scripts/addons/verify.sh packaging
```

The packaging tests use the actual skill-tree validator on Sandbox assets.
They check reference coverage, local links, notices, receipt hashes, and existing file and byte limits.
Package lifecycle tests cover install, upgrade, materialization, and uninstall.
Core tests cover stale built-in cleanup, preserved user overrides, and unchanged document extraction.

For guest execution, supply a real package whose guest image includes the document helpers:

```sh
sh scripts/addons/sandbox_smoke.sh --layout "$SANDBOX_DOCUMENT_LAYOUT" --require isolated
```

This smoke runs the generated PDF fixture checker through the real Sandbox command route.
It requires gVisor or KVM; an unavailable runtime is a failure, not evidence of helper execution.
The checker lives at `/usr/local/lib/localai/document-skills/check-document-skills.py` in the guest.
A stand-in image reference from `--local-check` cannot establish guest execution.

Run the focused UI checks with Node 22 and a dedicated server built from the current tree:

```sh
cd core/http/react-ui
node tests/unit/run.mjs chatskills.test.mjs
PLAYWRIGHT_EXTERNAL_SERVER=1 PW_WORKERS=2 ./node_modules/.bin/playwright test \
  e2e/chats-skills-picker.spec.js e2e/agents-skills-real.spec.js e2e/skills.spec.js
```

Build the UI and test server first, as described above. Start your own server on the configured free port.
The picker specs exercise rendering and switches; they do not alone establish real provider eligibility.
Also test actual provider resolution, grants, pins, OFF persistence, and index/load agreement against the application.
Require native linux/amd64 and linux/arm64 execution evidence before claiming release verification.
Cross-compilation or a configured CI matrix alone does not establish native execution.

## Upgrade submission and observation regressions

- [ ] `go test -race ./core/services/addons ./core/services/addons/gateway ./core/services/addons/recovery -count=1`
- [ ] `cd core/http/react-ui && node --test tests/unit/updatereview.test.mjs`
- [ ] `cd core/http/react-ui && npm run build`
- [ ] Verify cancellation during the real verified-backup path does not cancel
      an accepted upgrade or prevent its package commit.
- [ ] Verify status reads and same-ID retries through two real mTLS frontends
      remain available while backup exceeds an injected ordinary client deadline.
- [ ] Verify payload conflicts, actor isolation, terminal receipt replay after
      restart, interruption without replay, and Close cancellation/join behavior.
- [ ] Verify browser reload observes the stored ID, account switching isolates
      records, logout clears records, and progress does not invent percentages.

The recovery suite above remains required for crash boundaries during backup,
staging, migration and publication, including format-changing recovery-required
outcomes. The focused submission tests do not replace the real executable,
real-auth HTTP or rendered-browser acceptance gates.

### Current-read execution bindings

`ApplicationConfig.ChatSelectionSnapshot` returns synchronized, detached current
provider pins and host execution mode. Store reads preserve actor scope and return
detached records. JSON stores use their existing mutexes and atomic writes; SQL
stores use ordinary current reads. No revision ledger or directory lock is needed.

The gateway compares current selection inputs and runtime identity before dispatch
and after the reply. The reducer binds explicit actor, credential, conversation,
provider, tools and runtime identity. These checks are advisory, not a transaction
or a guarantee to detect revoke-and-restore changes between reads.

### Model action contract milestone

Run from the repository root (these packages share the root Go module):

```sh
go test -count=1 -v -tags auth ./pkg/addons/contract ./core/services/addons/apps ./core/services/addons/packages ./core/services/addons/runtime ./addons/internal/protocol ./core/config
```

`modelactions_test.go` covers strict keys, capability exclusion, traversal and
SDK path/count bounds, full manifest round trips and owning manifests. Apps
specs exercise actual installed projection and slice isolation. The package and
protocol declaration matrices include model actions independently and together
with all other extensions. Runtime specs verify exact registration equality,
crash recovery, receipt replay and immutable registry snapshots. Core/config
owns vocabulary parity; the contract package does not import core/config.
Exact contract, Apps, package and protocol spec names are in
`scripts/addons/suites.json`. These unit checks do not certify browser dispatch,
PostgreSQL services, or full-system harness prerequisites.


### Model launch and canonical route regressions

`core/http/react-ui/e2e/addon-model-actions.spec.js` exercises the production
React InstalledModels inspector, gallery Open and ModelPage Overview renderer.
Each surface covers arbitrary single providers, deterministic named collisions,
encoded selected model values, absence without install promotion and stale
installation refusal. The removed-route case asserts NotFound without redirect.
The tests mock HTTP responses; they do not verify backend authorization or
multi-node integration. Node tests in `addonmodelactions.test.mjs` cover bounded
metadata and dispatch rejection after actor, session, generation, entry or
page/path changes. `legacyroutes.test.mjs` retains native chat handoff checks.

The canonical-address fixture audit includes audio-transform, chats-switches-real,
chrome-audit, diarization-profiles, media-history, navigation, no-edge-rails,
page-render-smoke, sound-expansion, every studio spec, talk, taste-polish,
threed-animation, threed-gen and addon-navigation. Existing cases remain;
standalone addon addresses become declared fixture pages, and selected models
travel as query values. Addon-local navigation adapter coverage stays in place.

The execution-status viewer defaults to `viewer.html`. Use `-o /path/to/viewer.html`
to choose an output location explicitly; it does not read environment variables.
`-check` alone verifies the model without writing a file.
