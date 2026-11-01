<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.chartpatch`

Targeted patches of series references inside PRESERVED chart parts, and

## `CHART_NS`

```python
CHART_NS = b'http://schemas.openxmlformats.org/drawingml/2006/chart'
```

## `DRAWING_MAIN_NS`

```python
DRAWING_MAIN_NS = b'http://schemas.openxmlformats.org/drawingml/2006/main'
```

## `XDR_NS`

```python
XDR_NS = b'http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing'
```

## `parse_series_range`

```python
parse_series_range(text)
```

(sheet_title, range_part) for a sheet-qualified single-area range;
raises ValueError on anything else (external refs, multi-area, array
literals, unqualified ranges).

## `patch_chart`

```python
patch_chart(payload, sheet_title, operation, index, amount)
```

(new_payload, changed, blockers): rewrite every chart formula text
referencing ``sheet_title`` per the shift.

## `patch_chart_rename`

```python
patch_chart_rename(payload, old_title, new_title)
```

Rewrite every chart formula text (`<c:f>`) referencing ``old_title``
to ``new_title``. Returns the patched payload, or None
when nothing referenced the old title. Refuses when the chart carries
machinery whose references the patch cannot see (the same blocker set
as shift patching).

## `patch_chart_renames`

```python
patch_chart_renames(payload, mapping)
```

Rewrite chart formula texts through a SIMULTANEOUS title mapping
(title swaps must never merge reference classes).
Returns the patched payload or None when nothing matched.

## `patch_drawing_anchors`

```python
patch_drawing_anchors(payload, operation, index, amount)
```

(new_payload, changed): move drawing anchor markers with the cells
(from/to row and col elements in the spreadsheetDrawing namespace).

## `plan_chart_updates`

```python
plan_chart_updates(wb, sheet_title, operation, index, amount, overrides = None)
```

Plan every chart/drawing part patch for a shift on ``sheet_title``.

Returns (`{part_name: new_payload}`, blockers). Used twice: as a dry run
when the edit is attempted (blockers refuse before any mutation) and for
real at save time. ``overrides`` supplies already-planned payloads so a
part touched by an earlier shift is patched incrementally.

## `plan_property_edits`

```python
plan_property_edits(wb, ws, key, armed, current, original, allow_composed_formula_baseline = False)
```

A loaded chart's model drifted since arm: express the drift as byte
patches on the ORIGINAL part bytes, or refuse naming the first
property chartpatch cannot express. Expressible:
series/axis formula texts (`<c:f>`) and text runs (`<a:t>` — titles,
axis titles). Cached series values are left as-is: Excel re-reads
series from cells when it renders the chart.
