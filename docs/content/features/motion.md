+++
title = "Motion Capture"
url = "/features/motion/"
weight = 21
+++

LocalAI's `gemxcpp` backend converts timestamped RGB frames into human poses
using [gem-x.cpp](https://github.com/localai-org/gem-x.cpp). It runs resident
GEM-X, ViTPose and YOLOX models on CPU or Vulkan. Live inference does not need
SAM3D Body. This initial integration is API-only: no Motion UI, offline clip
export, body mesh or SONIC controller is included.

## Install and configure

Import `https://huggingface.co/LocalAI-io/GEM-X-GGUF` through the model importer.
It selects `gemxcpp` and downloads three checksummed, revision-pinned assets.
These are custom F32 GGUF architectures, not language models. Weight licenses
are described in the source model card. Allow memory for all three models and
execution buffers, beyond their roughly 4 GB combined download size.

Equivalent configuration with existing local files:

```yaml
name: gem-x
backend: gemxcpp
known_usecases: [motion]
parameters:
  model: gem-x/gem-x-contact-f32.gguf
threads: 4
options:
  - vitpose:gem-x/vitpose-f32.gguf
  - yolox:gem-x/yolox-f32.gguf
  - window:30
  - detector_interval:1
  - selection:continuity
  - precision:strict
  - max_gap_us:2000000
```

Additional options: `device:cpu` or `device:vulkan`, and `device_index:0`.
Without `device`, the package selects CPU or Vulkan according to its build.
Darwin uses CPU; Metal inference is not supported. Explicit unavailable devices
fail rather than silently falling back. Threads are capped at eight.

`window` accepts 2–120 observations; `detector_interval` accepts 1–30 processed
frames. Larger detector intervals reuse the previous crop and may miss movement
or person loss until the next detection. `selection:continuity` conservatively
associates one unique box; loss/ambiguity clears history. `selection:parity`
uses upstream's largest detection/full-image fallback and makes no identity
continuity guarantee. Neither mode is person re-identification.

`precision:strict` is the default. The launcher fixes GGML Vulkan precision
flags before initialization. `precision:backend_default` opts out of strict
validation, without promising a faster path. The same F32 model files are used.

## Sessions

All endpoints use standard LocalAI authentication, the **Motion Capture** feature
permission and model access controls. Sessions belong to their creating user.
When authentication is disabled, sessions share the unauthenticated principal.
Legacy admin keys have the existing shared legacy-admin identity.

```sh
curl http://localhost:8080/api/motion/sessions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gem-x","profile":"smpl24","time_origin":"camera session start"}'
```

The response is HTTP 201 with `id`, `model`, `profile`, `time_origin` and counters.
`profile` defaults to `soma77`; `time_origin` is required (1–128 characters).
A session owns its model's native temporal state. Only one session per model
is admitted, with up to 16 sessions per frontend and one duplex connection per
session. Additional model configurations consume independent model memory.

| Endpoint | Purpose |
| --- | --- |
| `POST /api/motion/sessions` | Create and load a session |
| `GET /api/motion/sessions/{id}` | Read frame/pose/drop counters |
| `DELETE /api/motion/sessions/{id}` | Cancel and release a session |
| `GET /api/motion/sessions/{id}/poses` | Exchange frames and poses over a duplex WebSocket |
| `POST /api/motion/sessions/{id}/tickets` | Issue a single-use browser WebSocket ticket |

Sessions expire after two minutes without frame activity. Inference also
has a two-minute timeout; initial model loading has a five-minute stream wait
limit, subject to the loader's own cancellation behavior. Native inference is
synchronous and cannot be interrupted mid-call. Closing stops new work and
discards late results; native cleanup completes after the current call returns.

Session state lives in one frontend process. Use sticky routing for all session
requests in multi-frontend deployments. Backend streaming uses the existing
model loader and keeps its selected gRPC connection for the session. There is
no session migration or recovery after frontend/backend restart.

## Duplex frame transport (v2)

The browser negotiates `localai.motion.v2` on the existing ticket-authenticated
`/poses` socket. It sends uncompressed binary protobuf `Input.frame` and receives
binary `Output` messages. HTTP remains for discovery, session creation, tickets
and deletion. There is no HTTP frame fallback. The protobuf package remains v1;
the v2 name versions the duplex transport and flow protocol.

Definition precedes a JSON flow hello. Each flow message has `type: "flow"`,
`version: 2`, a consecutive integer `id`, `release` containing decimal sequence
strings, `window_frames`, `max_pending_bytes`, `max_frame_bytes`,
`processing_ewma_ms`, `recommended_fps`, `queued_frames`, `queued_bytes`,
`oldest_age_ms`, `pressure` and cumulative `dropped_frames`. A completed frame
also has `completed_sequence` and `completed_kind` (`pose` or `event`). Its binary
output is written before its release; discarded inputs have release only.
While connected, every completed or dropped capture is retired once, and pose identity retains source time.

The initial window is two frames, with zero recommended FPS meaning camera-paced
subject to credits. After a service sample the window is eight. Service EWMA uses
alpha 0.2 and a 1ms floor; normal offered rate is 1.05/service, pressure rate is
0.9/service. Clients should gate capture before canvas/RGB allocation on credits,
outstanding bytes, pacing and prospective WebSocket upload backlog. A recommended
upload backlog limit is two frame sizes or 2MiB, whichever is smaller. Prefer fresh
camera callbacks over a fixed capture timer or a local RGB FIFO. Camera/upload
speed can limit the achieved rate. These are recommendations for external clients;
LocalAI does not include a browser capture client or enforce client-side pacing.

LocalAI uses one reader, one inference worker and one writer. Pending input is
bounded to eight frames/8MiB with a 2MiB encoded frame limit. At six frames or
6MiB, thinning retains half the pending frames nearest evenly spaced source-time
targets across oldest/newest (earlier ties; one survivor means newest). Active
inference is never thinned. Inputs older than 250ms since server admission expire
before enqueue/dispatch. Pressure clears once pending depth is at most two.
The 8MiB pending limit is not a total process-memory claim: encoded/decoded
active and reader payloads add up to approximately another 6MiB, excluding runtime,
protobuf and transport overhead. The writer queue holds at most eight messages;
writes time out at five seconds. External clients should also detect stalled
feedback; a five-second timeout is recommended.

A connection exclusively owns ingress: a second connection receives HTTP 409.
Only `localai.motion.v2` is accepted; missing or read-only subprotocols receive HTTP 400. The socket accepts
frames only; reset/camera/model changes require a new session. Disconnect cancels
transport, discards pending input and joins workers before releasing ownership;
this does not preempt an executing native GPU kernel. Only processed frames enter
existing `motion_frames_v1` accounting; dropped frames are not billed. Session
summaries expose upload accepted/dropped counts; ingress metrics distinguish
accepted, thinned and expired outcomes. Upload drop counts exclude pending/active
inputs disposed by disconnect; disconnect clears all browser credits. No per-frame content or credentials are logged.

## Frame schema and authentication

The authoritative schema is `pkg/motion/proto/motion.proto` in the LocalAI
repository, package `localai.motion.v1`. Generate client bindings from that file;
clients do not need LocalAI's internal backend protocol. Numeric arrays are
packed float32, joint-major then component. JavaScript clients must retain
64-bit sequence/timestamp fields as BigInt or their Protobuf library's integer
representation, not lossy JavaScript numbers for arbitrarily large values.

1. Create the session through HTTP JSON.
2. Connect to `/api/motion/sessions/{id}/poses` over `ws://` or `wss://`,
   requesting subprotocol `localai.motion.v2`. Native clients can use Bearer
   authentication; browsers use their authenticated same-origin session cookie.
   Successfully validated `Authorization`, `x-api-key` or `xi-api-key` header
   credentials bypass the WebSocket origin check. Invalid or unvalidated headers
   do not; cookie-only authentication still follows the origin policy. Standard
   browser WebSocket APIs cannot set these headers, so browser clients using
   cookies need an explicit CORS policy: set `LOCALAI_CORS=true`
   and `LOCALAI_CORS_ALLOW_ORIGINS=https://consumer.example,http://localhost:3000`.
   This explicit origin policy also permits cross-site session creation, tickets
   and deletion through CSRF protection. Authentication and permissions
   still apply. Allowed origins use the HTTP CORS matching rules, including explicitly
   configured wildcards. Without an explicit policy or validated header credentials,
   sockets remain same-origin even though HTTP uses a permissive default. Native clients without an Origin
   header are supported. Origin approval does not bypass authentication or session
   ownership; browser cookie availability still depends on cookie and browser
   policies. Credentials are not URL parameters.
3. Read the first binary message as `Output.definition`. It declares joint order,
   parents, rest transforms, coordinate conventions, profile and available channels.
   The definition is sent once per session connection. A disconnect ends the session;
   create a new session before reconnecting.
4. Send serialized `Input.frame` messages as binary WebSocket messages, following
   the flow credits described above. A frame contains tightly packed RGB8
   bytes, width/height, sequence, source microseconds and optional subject box/ID.
   Subject boxes use inclusive source pixel XYXY coordinates, bounded by
   `[0, width-1]` and `[0, height-1]`. Frames must be at least 8 pixels per side, at most 32766 per side and at most
   16 million pixels. RGB byte length must equal width × height × 3. The stricter
   2MiB encoded-message limit also applies to every upload.
5. Read `Output.pose` or `Output.event`, followed by the JSON completion release.
   The first usable observation produces `warmup`; at least two are needed for a pose.

Invalid frames, replayed sequences and non-increasing timestamps close the
connection without submitting that input to the backend. Source timestamps are
nonnegative microseconds relative to the declared origin and must strictly
increase, as must sequence numbers. Gaps in sequence numbers are allowed.
Create a new session before seeking backward, explicitly resetting or changing
cameras. Long source gaps, changed dimensions or a changed caller-selected
subject reset native context; poses/events carry the new epoch and reset flag.
Intrinsics follow upstream's image-centre/max-dimension approximation;
calibrated cameras are not supported by this ABI.

The model uses accepted-frame indices, not time-aware irregular sampling. Source
timestamps are preserved, not replaced by receive times or an invented FPS.
Consumers own resampling and clock alignment. Local processing durations do not
measure capture-to-consumer latency.

Pose and lifecycle outputs are delivered in order through the bounded writer
queue. A stalled reader eventually causes a write timeout and session closure;
outputs are not replaced by newer poses. WebSocket uses TCP, so already-buffered
bytes cannot be retracted. Reconnecting requires a new session; there is no
history replay.

## Browser WebSocket tickets

Browsers cannot set `Authorization` on a native `WebSocket` constructor. After
creating a motion session, exchange your normal credentials for a ticket:

```js
const response = await fetch(`${apiBase}/api/motion/sessions/${sessionId}/tickets`, {
  method: "POST",
  headers: {
    "Authorization": `Bearer ${apiKey}`,
    "Content-Type": "application/json",
  },
  body: JSON.stringify({ origin: window.location.origin }),
});
if (!response.ok) throw new Error(`Ticket request failed: ${response.status}`);
const { ticket, expires_at } = await response.json();
const wsBase = apiBase.replace(/^http/, "ws");
const socket = new WebSocket(`${wsBase}/api/motion/sessions/${sessionId}/poses`, [
  "localai.motion.v2",
  `localai.ticket.${ticket}`,
]);
socket.binaryType = "arraybuffer";
```

Cookie-authenticated clients can use `credentials: "include"` instead of the
Authorization header, subject to browser cookie policy. The ticket endpoint uses
normal authentication, Motion feature permission, model access and session
ownership checks. Its JSON response is HTTP 201 with `ticket` and `expires_at`,
and `Cache-Control: no-store`. When auth is disabled, tickets retain the existing
anonymous access policy; they do not create a user or grant additional access.

Tickets expire after **30 seconds**, are **single use**, and are bound to the
exact browser origin and session's pose WebSocket path. If the HTTP request has
an Origin header, it must match the requested origin. A reconnect requires a new
session and ticket. Redemption consumes the ticket before authentication/upgrade; even a
failed matching upgrade requires a fresh ticket. The original credentials are
revalidated, so revoked keys or sessions cannot redeem outstanding tickets.
Feature, model and ownership permissions are checked again. Successfully redeemed
tickets authorize the bound origin without requiring browser cookie delivery.

The server selects only `localai.motion.v2`; it never echoes the ticket protocol.
Tickets belong in the protocol list, not in the URL or query string. LocalAI
strips them before upgrade and redacts the protocol header from API traces.
Configure external proxies and request loggers to redact `Sec-WebSocket-Protocol`
as well. Ticket responses must not be cached or logged.

There are at most 32 outstanding tickets per user (shared for anonymous/legacy
identities) and 4096 per frontend; issuance returns 429 at capacity. Closing a
session revokes its outstanding tickets. The in-memory store is shared by ticket
issuance and upgrade on one frontend; both requests need sticky routing. Restart
invalidates all outstanding tickets. Ticket expiry limits the handshake window,
not the lifetime of an established connection. Ticket requests do not add frame
usage. This first integration supports motion; other WebSocket endpoints do not
yet issue tickets.

## Output profiles

**soma77:** 77 joints, metres, right-handed native SOMA Y-up. Positions have pelvis
translation removed only; root rotation is retained. Local rotations are XYZW;
local translations vary with the emitted frame's identity/scale. Root axis-angle
is in radians. Root translation is zero: this is not a continuous world trajectory.

**smpl24:** upstream-validated SOMA-to-SMPL mapping; 24 joint positions with pelvis
translation and anchor rotation removed. The separate anchor is a unit WXYZ
quaternion in the upstream Z-up/base-rotation convention. It reconstructs the
mapped, gravity-aligned points from root-local positions. No SMPL joint rotations,
mesh parameters, physical-base alignment or robot wrist commands are fabricated.

Definitions distinguish neutral reference transforms from per-frame shape.
Quaternions use upstream's canonical nonnegative-w policy, not temporal sign
smoothing; consumers interpolating rotations should handle equivalent signs.

Events include `warmup`, `lost`, `ambiguous` and `reset`. Frame flags identify
reset (1), reused crop (2), full-image fallback (4), detector execution (8) and
caller box (16). Track epochs mark acquisitions; automatic parity mode uses
track epoch zero without asserting identity continuity. No 3D confidence is
advertised. Consumers must detect stale streams and choose their own hold,
interpolation or stop behavior.

An external SONIC consumer can request SMPL-24, buffer timed poses and construct
its policy-specific reference windows. LocalAI does not execute that policy,
align references to physical robot state, or run physics.

## Observability

With metrics enabled, the existing `/metrics` exporter includes:

- `localai_motion_sessions`: active sessions.
- `localai_motion_frames`: processed frame outcomes.
- `localai_motion_uploads`: accepted, thinned and expired input frames.
- `localai_motion_resets`: explicit or native context resets.
- `localai_motion_frame_duration_seconds`: backend frame round-trip duration.
- `localai_motion_stage_duration_seconds`: detector, ViTPose, GEM and skeleton
  construction stage durations from the native pipeline.

Prometheus may append standard counter/unit suffixes. Labels use model/backend,
finite outcome names and fixed stage names, never session IDs or person IDs.
Frame/pose/drop totals are also available through session status.

Frame submissions also use the shared usage accounting pipeline, like animation
requests. Each successfully processed frame records `input_units: 1`; a pose
records `output_units: 1`, while warmup, lost and ambiguous outcomes record zero
output units. Records retain `accounting_rule: "motion_frames_v1"` in their usage
metadata. The existing usage dashboard, storage and billing metrics expose these
units through their legacy prompt/completion/total token fields; they represent
frames, not text tokens. Configure any model pricing in those units.

Accounting happens once per successfully processed WebSocket frame, before
socket delivery. Rejected or dropped inputs and failed inference do not record
usage. Session creation/status/deletion, tickets and WebSocket upgrades do not
record usage either. Shared statistics can be disabled through the existing
statistics configuration.

With backend tracing enabled, a `motion` trace spans the session and records its
profile, duration, termination reason and counts. Model-load failures use the
existing model-load traces. Logs record lifecycle changes. Images, pose arrays,
credentials and private model paths are not included in motion session traces.

### Webcam pose overlays

GEM-X pose messages optionally include `image_positions` (Protobuf field 13):
packed source-image pixel XY pairs in `Definition.joint_names` order (48 values for
SMPL24, 154 for SOMA77). The definition advertises the channel and convention
`source-pixel xy, joint_names order, top-left origin, unmirrored`. Coordinates
reference the exact submitted RGB Frame identified by the pose sequence/timestamp;
use that frame's width/height, then scale/letterbox with the displayed image. They
are neither normalized crop coordinates nor confidence scores. No image mirroring
is applied. Pixels can fall outside the source image; clients should clip drawing.

The backend projects GEM-X's emitted camera-space skeleton plus its camera
translation using the native live API's actual intrinsics: focal length
`max(width,height)` and image-centre principal point. SMPL uses the mapping from
that native definition, not a hard-coded substitute skeleton. If a joint has
nonpositive depth or projection is nonfinite the optional channel is empty; root
local control data remains separate. Older servers may omit the channel. Do not
invent an image-aligned overlay from root-local positions, anchor or bounding box.

### Optional live root displacement

The GEM-X SMPL stream advertises `root_displacement` only when its native library
supports channel 14. It contains three metres-per-interval components in the
**end pose's anchor-local basis**, not velocity or absolute world position. Rotate
by the end pose's WXYZ anchor for gravity-aligned Z-up displacement. The closed
interval is `[displacement_start_time_us, source_time_us]`, between consecutive
frames actually processed by GEM-X (including its warmup observation).

This uses the last two decoded world translations inside one rolling window;
it does not subtract absolute translations from separate windows. The current
frame's unobserved next-interval prediction is not emitted. `root_translation`
remains zero for compatibility. Older native libraries omit the new channel.

Consumers must establish a baseline after an epoch/track change, reset, missing
interval or reconnect; they must not add a delta across those boundaries. A
dropped input changes the model's accepted-frame spacing: these estimates are
not time-normalized or contact-refined, and integration can drift. Robot/world
placement is a separate consumer-owned transform and need not reset on tracking
loss. Model-backed checks cover interval boundaries and coordinate-basis
consistency; they do not establish trajectory accuracy against ground truth.
