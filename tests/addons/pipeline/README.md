# Pipeline browser acceptance

This harness mounts the production add-on UI with a small SDK transport adapter.
The loopback HTTP server uses the production SQLite store, artifacts, state machine,
and asynchronous runner. Only model inference is mocked: prompt review, PNG image
generation, segmentation, and a valid one-triangle GLB. A deliberately failing
image model exercises failure inspection and retry.

From the repository root, with the React UI dependencies installed:

```sh
nix develop
npm ci --prefix addons/asset-pipeline/ui
node tests/addons/pipeline/build.mjs
node --test addons/asset-pipeline/ui/test/*.test.mjs
go build -buildvcs=false -o /tmp/localai-pipeline-test-server ./tests/addons/pipeline/cmd/server
/tmp/localai-pipeline-test-server -data /tmp/localai-pipeline-browser-data
# In another dev-shell terminal:
node tests/addons/pipeline/acceptance.mjs
node tests/addons/pipeline/creation.mjs
node tests/addons/pipeline/references.mjs
node tests/addons/pipeline/animation-flag.mjs
node tests/addons/pipeline/activity.mjs
node tests/addons/pipeline/selection.mjs
node tests/addons/pipeline/workbook.mjs
node tests/addons/pipeline/autoscroll.mjs
node tests/addons/pipeline/previews.mjs
node tests/addons/pipeline/extended.mjs
```

The server binds `127.0.0.1:8097`. `PIPELINE_TEST_URL` overrides the browser target.
The flake supplies `PLAYWRIGHT_CHROMIUM_PATH`. Tests create uniquely named pipelines
and save screenshots under the ignored `results/` directory. The harness does not
replace host authorization, distributed ownership, or live-backend integration tests.

For an explicitly requested live-backend smoke test, run `node
tests/addons/pipeline/live.mjs`. By default this uses the Docker instance at
`http://127.0.0.1:8081`, `Z-Image-Turbo`, and `trellis2-4b`. Override `LOCALAI_URL`,
`PIPELINE_IMAGE_MODEL`, or `PIPELINE_3D_MODEL` as necessary. The test performs real
inference and saves an image and a validated GLB in a new temporary directory. It
does not install models, change server configuration, or force model unloading.
This checks the live generation APIs, not deployment of the new add-on into that
server; the packaged add-on requires the matching new host implementation.

The browser checks prompt approval and rejection, inline edits, candidate selection,
visible processing status, extraction, GLB rendering and downloads, preview cleanup,
reload, read-only history inspection, persistent event paging, rewind, branching
from old executions, failure styling and explicit reruns, incompatible engines,
and uploaded images bypassing text/image generation. It also checks foreground and
Shift-click background segmentation coordinates, explicit cancellation, a valid
PNG spanning several upload chunks (including exact byte preservation), and
importing the same image through the LocalAI media selector. `npm test` in the add-on UI
directory separately tests draft dependency invalidation and next-node selection.

The workbook browser suite checks explicit per-card saving (including absence of
autosave), independent unsaved drafts, descriptive checkpoints, primary-output
saving, Run/Rerun readiness, fresh Run all branches, and Continue preserving prior
attempts. It exercises save failures, serialized saves, lost-response
reconciliation, conflict recovery preserving both branches, and explicit
keep/discard navigation. It also checks active-chain connector geometry at desktop
and mobile widths, skipped inactive stages, and reduced-motion behavior.
Screenshots are saved in `results/workbook-desktop.png` and
`results/workbook-mobile.png`. The transport returns the production apply-result
shape and selects the runner's result branch for workbook execution commands.

The autoscroll suite checks new output following, manual opt-out at the bottom,
debounced re-enabling after scrolling away and back, delayed preview growth,
keyboard operation, and the mobile output pane.

The creation suite checks name-field/button alignment, matching control heights,
viewport fit at desktop and mobile widths, and creation from the expanded form.

The activity suite checks the bottom-pinned running task and spinner, scrolling
with autoscroll disabled, mobile fit, reduced motion, cancellation and completion.

The selection suite checks restoring the last chosen or created pipeline across
refresh/navigation, cancelled navigation, missing resources and blocked storage.

The previews suite checks selected-tab styling, pending-selection cards, and
automatic loading of all four image candidates despite temporary file-service failures.

The extended suite checks remeshing, rigging, motion generation and retargeting
with generated two-joint skeleton and skinned-triangle GLB fixtures. It exercises
visible skeleton overlays, per-joint weight heatmaps and original-material restoration,
numeric validation, distinct DAG join lanes, independent branch invalidation,
animation playback and scrubbing, downloads, unload/reload and old-preview
retirement, and opt-in extension of legacy five-card pipelines. These fixtures
test UI/state behavior, not the quality of actual model inference.

After explicitly authorizing deployment and real inference, `live-extended.mjs`
creates a uniquely named QA pipeline on the live add-on page; it never selects or
edits an existing user pipeline. It uploads an unrigged, vertex-color GLB and runs
remesh → rig, text → motion, then joins them with Apply motion. It saves downloaded
GLBs and canvas screenshots in `results/`, checks playback and reload, and retains
the QA pipeline for inspection. Set `RUN_LIVE_PIPELINE_QA=1`, `PIPELINE_TEST_MESH`,
and `PIPELINE_MOTION_MODEL`; optionally set `LOCALAI_URL`, `PIPELINE_REMESH_MODEL`,
`PIPELINE_SKIN_MODEL`, `PIPELINE_REMESH_MODE`, `PIPELINE_TARGET_QUADS`, and
`PIPELINE_MOTION_STEPS`. This test requires the extended add-on and installed live
backends, and can take several minutes. Do not run it as part of mock CI checks.

The animation-flag suite checks the default-off Animation (WIP) switch, keyboard
operation, persistence, preservation of saved graphs, blocked animation runs,
and desktop/mobile presentation in both themes.
