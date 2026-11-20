---
title: "3D Asset Pipeline"
description: "A resumable, inspectable asset workflow from text or images to GLB."
---

The experimental `asset-pipeline` add-on provides a split-pane workflow for creating
3D assets. It requires add-on API version 2, a host built with the pipeline host operations and an owner
policy granting this add-on `pipeline.` operations. The official plugin library
provides Linux x86-64 packages after the publishing workflow completes on master
or a release tag. Install suitable models separately.

On the Add-ons page, pin **3D Asset Pipeline** to show it in the left sidebar,
identified by its white 3D cube on a teal tile.

Expand **Create a pipeline**, enter a pipeline name, then click **New pipeline**.
The name field and button align side by side, wrapping on narrow screens.

The last selected pipeline is remembered in this browser for this LocalAI site,
including newly created pipelines, across navigation and page refreshes. This is
a browser-local preference, not an owner-wide setting or a cross-device sync.
Only pipelines in your current accessible list can be restored; if the remembered
pipeline is unavailable, the first available pipeline is selected. When browser
storage is disabled, selection still works but cannot be remembered.

While a stage is running, a highlighted activity strip at the bottom of the status
pane names the active task and shows a spinner. It remains visible when you scroll
back through outputs or disable autoscroll, and clears when processing stops.

## Stages

1. Enter text in Asset description. To skip image generation, choose a direct
   image input on Object extraction or 3D generation and upload PNG/JPEG or
   import an image from this browser's LocalAI image history.
2. Optionally ask a chat model to suggest a prompt and explain its changes. Accept,
   edit, or keep the original. TRELLIS models get game-asset/black-background advice.
3. Generate one to four images, then choose a candidate. Multiple candidates always
   require selection; one candidate can continue automatically with pause disabled.
4. Keep the image unchanged or use a segmentation model returning masks. A text
   prompt and positive/negative image clicks can guide extraction. Support for these
   inputs depends on the segmentation backend. Extracted outputs retain transparency.
5. Generate a GLB with an image-conditioned 3D model, such as TRELLIS.2.
6. Optionally remesh with `mesh-remesher`, independently of generation weights.
7. Generate a skeleton and learned weights with `skintokens-f16`, or use **Skin to
   skeleton** with a supplied rig or motion skeleton.
8. Enter a motion description, optionally review it with a chat model, then
   generate a skeleton-motion clip with Kimodo. **Apply motion** joins the rig
   and motion branches to produce a compatible animated humanoid GLB.

Remeshing, rigging, weights and animation are being integrated (see status below).
New pipelines include their optional cards, initially inactive because 3D generation
remains the primary output. The initial 3D adapter
requires images; declaring a text input in a graph does not add text-to-3D support.
Prompt advice currently uses a fallback profile, not arbitrary gallery metadata.

### Remeshing, skinning and animation

The state engine supports named secondary inputs for the skinning
and motion-application stages. A join records both resolved inputs in its attempt,
runs each shared ancestor once, and becomes stale when either input branch changes.
The existing `input` binding remains unchanged for saved single-input pipelines;
secondary bindings use `inputs.skeleton` (skinning) or `inputs.motion` (applying
motion). These bindings must reference earlier, type-compatible nodes or direct
artifacts belonging to the same saved pipeline. Empty artifact references cannot
execute. Installing the matching backend builds is required before running these
stages; a saved graph alone does not install models or backends.

The motion executor can use the existing Kimodo animation backend. It enforces
the same advertised prompt and parameter limits as `/3d/animate`, records a
downloadable skeleton-motion GLB separately from mesh outputs, and participates
in the normal authorization and best-effort model-lease cleanup. Motion seeds
must be non-negative. This adapter does not itself skin or animate a mesh; use
the separate skinning or motion-application card for that operation.

The SkinTokens native adapter uses pinned-source CPU and Vulkan builds.
An adapter-level material transfer preserves embedded textures, UVs, vertex
colours and PBR settings for single-primitive GLBs; unsupported layouts fail
explicitly. This compensates for upstream's texture-flattening exporter and has
fixture coverage and a real-model Vulkan acceptance test covering generated
rigging, supplied-skeleton skinning and motion retargeting. Its generated-rig
retargeter accepts recognized humanoids with SOMA30 motion, not arbitrary
rig/motion pairs. Model discovery advertises mesh, skeleton and motion inputs
separately from Kimodo's text-to-motion input.

The executor now routes `rig`, `skin` and `retarget` stages through the native
backend using the recorded mesh and secondary artifact inputs. These stages use
the same ownership checks, permission rechecks, saved GLB outputs and best-effort
model leases as earlier stages. Their cards expose typed previous outputs or GLB
uploads, model selection by supported operation, and generation parameters. Motion
description and motion prompt review are separate from image-conditioning prompts.
The skin-to-skeleton card is an alternative to generated rigging; select it as the
primary output to skin directly to a supplied rig or generated motion skeleton.

The `skintokens-f16` model gallery entry installs the complete F16 mesh encoder,
TokenRig and SkinVAE bundle with revision-pinned, SHA-256-checked downloads.
Importing `https://huggingface.co/LocalAI-io/SkinTokens-GGUF` selects the same bundle.
The native backend is `skintokenscpp`; its new OCI images must be built/published
(or installed from a local build) before a remote gallery installation can work.
When installing backend archives with the CLI beside an already running server,
restart that server after installation so it discovers the new backend executables.
`POST /models/reload` refreshes model configurations, not the backend registry.
Static rigs that contain only a zero-duration pose clip do not show animation
playback controls; actual motion clips expose playback and scrubbing.
No new engine preference is assigned: there is no competing backend for these
weights to rank against.

A standalone `meshremesh` backend separates
Alpha Wrap, quad-dominant remeshing and material projection from TRELLIS model
inference, so geometry processing requires no generation weights. Its packaged
gRPC backend supports `wrap`, `quad` and `wrap_quad`; requests save quality
statistics in the output GLB's `extras.localai_remesh` and backend logs.
Quad-dominant results are triangulated for GLB and may have boundary edges;
they are not automatically watertight or print-ready. Inputs currently must be
single-primitive, untransformed vertex-coloured GLBs, not texture-atlas or already
skinned meshes. Unsupported inputs fail explicitly rather than losing materials.
The gallery configuration is `mesh-remesher`; like the SkinTokens backend, the
new backend images require publication or local installation before use. The
pipeline executor and card support the stage.

The optional remesher contains GPL-3.0-or-later CGAL Alpha Wrap code. Its gallery
entries and packaged `NOTICE` identify this explicitly. It remains a separate
backend executable; the asset-pipeline add-on does not bundle that native code.
Retain the dependency notices and meet the applicable source-distribution
requirements when redistributing a backend build.

Motion and skinned-mesh previews provide play/pause, a time slider and clip
selection when a file contains several clips. Skeleton-only motion displays
joint connections. **Follow character** keeps locomotion in view; turn it off for
a fixed world view, or use **Reset view** to reframe the character. Playback
renders at most 30 FPS, suspends off-screen or in a
hidden browser tab, and releases GPU resources when unloaded. Older 3D outputs
start unloaded; meshes and motion clips remain downloadable as GLB. Previewing
does not fetch external glTF resources. The source build needs
`npm ci --prefix addons/asset-pipeline/ui` for its pinned animation renderer.

The UI's dependency and execution checks handle both branches of a join: a stale
motion input disables retargeting even when its mesh input is current, and editing
motion does not invalidate an independent mesh branch. Only edges in the primary
output's dependency graph are displayed.

Completion of the extended workflow includes deployment and live-instance QA,
after mock and backend tests: remesh an asset, generate its skeleton and weights,
generate motion, apply compatible motion, and inspect/download the animated GLB.

## Configuration and execution

Select the primary output on a node card. Only its input ancestors execute; the
other cards are inactive. Inputs can be direct values or a previous node's output.
Stage headings and common actions have line icons alongside their text labels;
icons are decorative and do not replace accessible names.
Only processing stages can be primary outputs. The description is a text-only
source, not an output stage. Input choices include only earlier stages with a
compatible output type; the owner validates the same rules before execution.
Cycles and incompatible input types block execution. Changing a node makes it and
its dependants stale. Changing engine advice adds a warning without rewriting or
invalidating upstream prompts.

Each card has **Save**, enabled only when that card has unsaved changes. Saving
records a detailed checkpoint and invalidates dependent outputs; it does not
start inference or save edits on other cards. The selected primary-output card
also saves a changed primary-output choice, and the 3D card saves its engine advice
and input capabilities. The description card supplies saved text and has no Run
button.

Processing cards have **Run** for unrun/stale results or **Rerun** for an existing
attempt. These buttons execute that stage immediately using saved configuration.
Unsaved edits on the stage or its ancestors block execution, as do stale or
unaccepted processing inputs. Unrelated inactive drafts do not block a ready
stage. Rerun preserves the previous attempt in a new branch; a result already
invalidated by an earlier edit can be run without another redundant retry branch.

**Run all** starts a new execution branch and restarts every stage in the active
chain, without reusing cached outputs. It pauses for review or candidate selection
as configured. **Continue** resumes that execution after a selection without
restarting its completed stages. Use the failed stage's Run/Rerun to recover from
an error or cancellation, then Continue. Execution status and outputs are always
recorded automatically; only configuration saving is explicit.

The Run all preview lists stages through the first review pause without marking
completed cards as pending work. A separate Continue preview shows the remaining work in an
ongoing execution. The next card and the enabled run buttons
share an accent border. A brief pulse draws attention to the next card and is
disabled when reduced motion is requested. Right-hand arrows connect only the
active chain to the primary output, bypassing unused cards.
Missing descriptions, images and models are shown on the active stage cards;
Run all checks configuration across the active chain before starting.
Extraction controls are shown only in segmentation mode. Include/exclude image
clicks have visible markers and undo/clear controls; points are tied to their
input image, so an upstream image change requires clearing old points.
The status view records configuration checkpoints, resolved inputs, progress,
outputs, errors and cleanup diagnostics. LLM output streams as bounded text;
3D execution reports activity without expensive intermediate mesh renders.
Source descriptions are read-only in the timeline, while prompt-review decisions
are editable. Accepted outputs show their selection; use Change selection to
correct one. A pending-output shortcut brings the decision into view. Image
candidates are numbered and arranged for comparison.

The selected Nodes/Executions tab has an accent background and underline. A stage
waiting for a decision has an amber border and a **Choose an image** or **Review
output** action; green marks the most recently successful stage, not pending work.
New image candidates load automatically through a transfer queue. Temporary file
service failures are retried twice; persistent failures show **Retry preview**
with technical details separately, without requiring regeneration.

The output pane follows new status, streaming text and loaded previews while
**Autoscroll** is checked. Its checkbox and arrow icon stay below the scrollable
history. Scrolling up pauses following; returning to the bottom re-enables it
after a 250 ms debounce. Manually unchecking it at the bottom keeps it off until
you scroll up and return, or check it again yourself.

Configuration is not autosaved. A visible indicator distinguishes unsaved, saving,
saved and failed changes. Each Save checkpoint describes the fields and old/new
values; full values remain in its snapshot when the message abbreviates long text.
Navigation with unsaved edits offers **Keep editing** or **Discard changes** and
never saves implicitly. Failed saves retain the draft; uncertain requests can be
reconciled without duplicating checkpoints or inference. If another session
changed the saved revision, **Save edits in new branch** preserves that execution
and saves the affected card separately, retaining other local drafts. Pending
edits are browser-local until saved, so do not close the tab while it reports
unsaved changes.

History inspection is read-only. Rewinding followed by an edit or execution forks
from that checkpoint; a page-level notice distinguishes historical state from
the current execution. Saving a historical card or executing from it creates a
new branch. Inspecting or editing without saving does not create one. An owner
restart marks unfinished attempts interrupted and never automatically repeats
inference. Cancel is cooperative: the backend must acknowledge cancellation before
the attempt finishes. A generation seed is recorded but does not guarantee identical
output across model/backend versions.

Models receive advisory execution leases. Cleanup is best effort: ordinary users
cannot force unload, and cached, busy, pinned, shared or remotely routed models are
retained. See [model leases](../model-leases/).
Motion generation also carries its execution lease through model loading, so a
locally loaded Kimodo instance is eligible for the same authorized cleanup when
the stage ends; it is not intentionally kept resident for later pipelines.

Rigged mesh previews show a skeleton overlay through the surface by default. Use
**Show skeleton** to hide it, or choose **Skin weights** under **Surface view**
and select a **Weight joint** to inspect that joint's vertex influences (blue is
zero, red is full influence). **Original mesh** restores the original materials
and textures. These controls only affect the preview, never the downloaded GLB.

The latest GLB output on the current execution opens automatically; older and
historical previews stay unloaded until requested. Unload preview releases its
browser resources and remains unloaded across status updates.
Downloads use authenticated private artifacts. Original artifacts remain in owner
storage even when a temporary download handle expires.

## Storage, permissions and bounds

State is owner-wide, not frontend-local. Frontends dispatch authenticated resource
operations to the singleton add-on owner. SQLite persists an append-only event
journal, branch links and idempotency receipts alongside immutable artifact files.
New events contain one flat operation and belong to one of three categories:
configuration (node ID, field path, value type and value), execution (start,
progress, finish, selection or invalidation), or observed system information.
They do not contain a graph snapshot or a list of arbitrary changes. For example,
changing the image count records `/count` for that node, not the entire graph.
State at a checkpoint is reconstructed by replaying those operations through
`ApplyEvent`; replay never invokes models or other external work. Timeline states
sent to the browser are reconstructed views, not the persistence format.

An attempt records one invocation and its resolved arguments, progress, outputs,
and outcome. Node readiness is separate: a successful attempt can supply a stale
result after an upstream edit, or await the user's output selection. Neither case
changes the invocation's successful outcome. Attempt arguments are recorded once;
progress events refer to the invocation by ID. Replay validates the captured node
configuration and resolved inputs against the state at invocation start; an
inconsistent invocation is rejected instead of being executed or restored.

Forks reference their parent's event prefix and append only their own events.
The timeline includes the inherited prefix without copying it into the child log.
A fork does not cancel parent inference or inherit ownership of a running job:
that node is stale in the child, while the original attempt continues on the parent.
Later parent events do not change the child's checkpoint. Cancellation requests
are recorded before signalling the executor; completion records the actual result.

Only the current flat-event journal format is supported. Earlier snapshot-based
and batched-operation histories are rejected, not migrated. Back up and use a
fresh pipeline data directory when replacing a development build that wrote those
formats; the server does not automatically delete or reset existing data.
Idempotency receipts pin a log prefix, so a repeated request returns its original
result without replaying inference or including later events. The new stored format
requires this version of the server; back up the owner data before downgrading.
Periodic persisted snapshot caches are not required by this implementation: the
event log alone is sufficient to restore state.

Configuration-only replay filters out execution and system events. Configuration
exports therefore have no attempt, output, error or selection state. The JSON
vocabulary lives in `pkg/assetpipeline/schema`; runtime nodes are separate concrete
Go types. Their inputs hold typed producer pointers (for example, an image input
requires an `ImageProducer`), and resolve the selected output only when it is
accepted and not stale. Mesh assets currently support the enumerated GLB format.
Configuration values are decoded using their declared field paths, including exact
64-bit seeds, before entering the interpreter. Runtime configuration, events and
attempts use separate Go records; copying state does not serialize it. Public
history reads return detached copies so callers cannot rewrite recorded events.
Each branch retains its own configuration and execution state, with the connected
runtime graph derived from them. Incomplete or
incompatible configurations are preserved losslessly as editable drafts; they
cannot execute until their inputs and settings form a valid graph again. Replacing
a node in a graph at the 32-node limit is supported: removed nodes are applied
before their replacements.
Execution failures retain a structured code, message and wrapped cause; cancellation
is identified from the Go error chain, not by matching its message.

The UI currently consumes materialized configuration/state views as a loading
optimization; it does not yet maintain a JavaScript event reducer. Status cards
group the flat events of a command at its final checkpoint. Journal entries remain
immutable; no compatibility interpreter or snapshot migration runs during loading.

Optimistic revisions reject stale concurrent edits. Each stage checks current user,
feature and model permissions before inference, and cleanup checks permissions again.
The user needs the 3D feature plus the stage's chat, image or detection permission.

Uploads use resumable chunks, accept PNG/JPEG up to 20 MiB and 16 megapixels, and
self-contained GLB 2.0 files up to 128 MiB. GLBs with external buffer or image
references are rejected; backend-specific geometry validation happens when the
stage runs. Upload storage has a 256 MiB quota per pipeline. Saved assets have a 512 MiB
per-file bound and a 4 GiB per-pipeline quota. History is bounded at 64 MiB;
create another pipeline before reaching this limit. The initial version does not
automatically garbage-collect historical assets or abandoned uploads.

The host's private artifact transport supports files up to 512 MiB while retaining
its existing 1 GiB per-user aggregate quota, concurrency limits and timeouts. Large
downloads can still time out on slow links. Loading a GLB may consume substantially
more memory than its compressed file size.

## Development checks

### UI review guidelines

- A control must match the stage's purpose, input type and place in the workflow.
  Prevent impossible choices rather than presenting them and failing afterward.
- Source entry is not processing. Keep text entry separate from image import,
  and offer only processing stages as primary outputs.
- Make selection state obvious: a primary-output control belongs at the card's
  top right, with a bordered, fully clickable label and a visible radio indicator.
- Explain the next action and missing prerequisites close to the relevant stage.
  Do not require a failed inference request to reveal missing configuration.
- Keep common actions visible; disclose technical parameters and raw diagnostics
  only when needed. Preserve access to all recorded inputs, outputs and failures.
- Distinguish drafts, historical inspection, stale results and pending decisions.
  Navigation must not silently lose edits; image-specific controls must not carry
  coordinates onto a different image.
- Treat previews as managed resources: show the latest result, allow explicit
  unloading, and avoid loading historical 3D assets without a request.
- Review the rendered UI at desktop and narrow widths and exercise decisions,
  retries and history with mock inference. Passing unit tests alone is not UX
  acceptance.

### Test harness

Use the Nix flake for Go tests and UI builds. `tests/addons/pipeline` contains a
browser acceptance harness running the actual pipeline runner and SQLite store
with mock inference. No model weights or container are needed for that harness.
Build the UI with `node addons/asset-pipeline/ui/build.mjs`; build a local package
with `scripts/addons/package.sh --config addons/asset-pipeline/package/platforms.yaml
--layout /tmp/asset-pipeline-oci --version 0.1.0 --platform linux/amd64 --local-check`.
The executable uses SQLite and requires CGO. Other platforms remain disabled until
their native packaging checks have run.

### Reference images and library selection

Image generation can combine its text description with an optional **Reference
image**. Choose **Upload or choose an image** to supply a PNG/JPEG, or select an
image output from an earlier node. Choose **None — text only** to remove it.
Use a model configured for image editing or reference conditioning: image
capability alone does not establish that a model supports reference images.
The reference is sent through the backend's `ref_images` input.

The named `inputs.reference` binding participates in execution ordering, saved
arguments, replay and staleness just like the skeleton and motion inputs of
multi-input stages. Changing the reference invalidates dependent results.
Uploaded and library images are copied into pipeline artifact storage.

**Choose from library** opens a preview picker with search, pages of thumbnails,
and an explicit **Use selected image** action. Reference images use the same
picker. The library contains Studio image history saved in the current browser;
it is not a server-wide asset listing. Missing images show an unavailable preview
and failed imports leave the picker open for another selection.

### Animation (WIP)

The **Animation (WIP)** slider enables the experimental rigging, skinning, motion
prompt, motion generation and motion application cards. It is off by default;
image generation, mesh generation and remeshing remain available.

The switch applies immediately and remembers the choice in this browser. It is a
UI feature flag, not a server permission or shared pipeline setting. Saved nodes,
configuration, outputs and history are retained when it is off. If a saved
pipeline's output depends on animation, Run all and Continue are blocked until
you enable the switch or save a non-animation primary output. Individual runs
with animation dependencies are blocked too. Save or discard edits before
changing the switch; it is unavailable while a job or save is in progress.

### Plugin gallery

The official plugin library includes **3D Asset Pipeline** for Linux x86-64.
The package workflow builds, verifies and signs the plugin before publishing its
development tag on master and versioned packages for releases. Other platforms
remain disabled until their native package checks have passed.
