# VRAM "Fits" Feature - Backend Optionals (Task 3 and 4)

Issue: https://github.com/mudler/LocalAI/issues/9248

This document captures the optional backend work after the UI-only implementation:
- Option 3: backend-side filtering for models that fit GPU VRAM
- Option 4: persisted fit-filter preference (server-side default)

## Current Baseline

Already in place:
- UI filter toggle on Install Models page
- Client-side filtering based on:
  - `GET /api/resources` (available GPU memory)
  - `GET /api/models/estimate/:id` (model VRAM estimate)
- Toggle persisted client-side in browser local storage

Current backend endpoints involved:
- `GET /api/models` in core/http/routes/ui_api.go
- `GET /api/models/estimate/:id` in core/http/routes/ui_api.go
- `GET /api/resources` in core/http/routes/ui_api.go

## Option 3: Backend "Fits" Filter

### Goal

Allow filtering directly in `GET /api/models`, for example:
- `GET /api/models?fits_gpu=true&context=8192`

This enables server-driven filtering and allows non-React clients to reuse the same capability.

### Recommended API Contract

Query params to add in `GET /api/models`:
- `fits_gpu` (bool): when true, return only models that fit available GPU memory
- `context` (int, optional): context window used for estimate (default 8192)
- `fit_margin` (float, optional): safety margin (default 0.95)
- `fit_mode` (string, optional):
  - `strict`: include only models with explicit estimate that fit
  - `optimistic` (default): include unknown estimates too, filter only explicit non-fits

### Implementation Touchpoints

Primary file:
- core/http/routes/ui_api.go

Supporting packages:
- pkg/vram (estimation)
- pkg/xsysinfo (GPU memory)

Use existing estimate model builder in `ui_api.go`:
- `buildEstimateInput(...)`

### Suggested Flow in /api/models Handler

1. Parse new query params near existing term/tag/page/items parsing.
2. If `fits_gpu != true`, preserve current behavior exactly.
3. If enabled:
   - read aggregate GPU memory from `xsysinfo.GetResourceInfo()`
   - resolve context (default 8192)
   - for each candidate model:
     - build estimate input via `buildEstimateInput(model)`
     - run estimate for one context
     - compare `vramBytes <= totalMemory * fit_margin`
     - apply `fit_mode` rules
4. Continue with existing sorting/pagination logic.

### Performance and Caching Guidance

Without cache this can be expensive because `/api/models` is paged but filtering is done pre-pagination.

Recommended cache:
- key: `modelID + context`
- value: estimate result + timestamp
- ttl: 5-30 minutes
- invalidate: when galleries refresh or process restart

If you want a simple first step:
- add in-memory cache map in `ui_api.go` closure scope with mutex
- move to pkg/vram cache abstraction later

### Minimal Go Skeleton (inside /api/models handler)

```go
fitsGPU, _ := strconv.ParseBool(c.QueryParam("fits_gpu"))
fitMode := c.QueryParam("fit_mode")
if fitMode == "" {
    fitMode = "optimistic"
}
fitMargin := 0.95
if raw := c.QueryParam("fit_margin"); raw != "" {
    if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 && v <= 1 {
        fitMargin = v
    }
}
ctxSize := 8192
if raw := c.QueryParam("context"); raw != "" {
    if v, err := strconv.Atoi(raw); err == nil && v > 0 {
        ctxSize = v
    }
}

if fitsGPU {
    info, err := xsysinfo.GetResourceInfo()
    if err == nil && info.Available && info.Aggregate.TotalMemory > 0 {
        totalMem := float64(info.Aggregate.TotalMemory)
        filtered := make(gallery.GalleryElements[*gallery.GalleryModel], 0, len(models))
        for _, m := range models {
            input := buildEstimateInput(m)
            if !input.HasWeights() {
                if fitMode == "optimistic" {
                    filtered = append(filtered, m)
                }
                continue
            }

            est, err := vram.EstimateForContexts(c.Request().Context(), input, []int{ctxSize})
            if err != nil {
                if fitMode == "optimistic" {
                    filtered = append(filtered, m)
                }
                continue
            }

            e := est.Estimates[strconv.Itoa(ctxSize)]
            if e.VRAMBytes <= uint64(totalMem*fitMargin) {
                filtered = append(filtered, m)
            }
        }
        models = filtered
    }
}
```

Note: exact estimator call name should match current `pkg/vram` API in this branch.

### Tests to Add (Option 3)

Add route tests under:
- core/http/routes

Suggested new file:
- core/http/routes/ui_api_models_fit_filter_test.go

Test cases:
- no `fits_gpu` param => unchanged behavior
- `fits_gpu=true` + explicit estimates => non-fitting models removed
- `fit_mode=optimistic` keeps unknown-estimate models
- `fit_mode=strict` removes unknown-estimate models
- `context` changes fit result
- no GPU detected => graceful fallback (either no filtering or empty based on decision; document behavior)

Also add API contract notes to docs if exposed to users.

## Option 4: Persisted Filter Preference (Server-side)

You already have browser persistence in UI local storage.

Option 4 backend variant is useful when you want a server-controlled default for all admins/users.

### Option 4A: Global Runtime Setting (Fastest)

Add a runtime setting in:
- core/config/runtime_settings.go (field in `RuntimeSettings`)
- core/config/application_config.go
  - include in `ToRuntimeSettings()`
  - apply in `ApplyRuntimeSettings()`
- config struct storage location for live value (ApplicationConfig)

Then expose in existing settings APIs:
- `GET /api/settings`
- `POST /api/settings`

Because settings endpoint already marshals RuntimeSettings, adding the field is usually enough once ApplicationConfig mapping exists.

Proposed setting name:
- `models_fits_gpu_filter_default` (bool)

UI behavior with this setting:
- initial state = localStorage value if present
- otherwise fallback to `/api/settings` default

### Option 4B: Per-user Preference (Best UX, More Work)

Use a user profile/preferences store (if available in your auth stack) so each user gets independent defaults.

If there is no profile-preferences API yet, Option 4A is a clean first step.

## Frontend Integration for Option 4

File:
- core/http/react-ui/src/pages/Models.jsx

Current initialization:
- reads from localStorage key `localai-models-fits-filter`

Extension plan:
1. fetch settings (`settingsApi.get()`)
2. if localStorage is unset, use server default
3. keep writing localStorage on user toggle
4. optionally add "Use server default" tri-state behavior later

## Suggested Delivery Plan

1. Ship current UI-only solution (already done)
2. Add Option 3 backend filter with optimistic mode default
3. Add Option 4A global runtime setting
4. Add route tests for Option 3 and config tests for Option 4A
5. Add docs note for `/api/models` new query params

## E2E/Validation Checklist

- UI:
  - toggle appears only when GPU memory is available
  - enabling toggle hides explicit non-fit models
  - unknown-estimate models remain visible in optimistic behavior
  - toggle persistence works across reload
- API Option 3:
  - verify `/api/models?fits_gpu=true&context=8192`
  - verify strict vs optimistic behavior
- Settings Option 4A:
  - `POST /api/settings` persists default
  - `GET /api/settings` returns same value

## Notes

- Keep API backward compatible by making all new params optional.
- Avoid blocking `/api/models` response with slow estimator calls; add cache early.
- If adding public API behavior, update docs under docs/content as part of final PR.
