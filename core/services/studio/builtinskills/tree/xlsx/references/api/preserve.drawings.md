<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.drawings`

Charts and images added in-session become NEW drawing/chart/media parts

## `CHART_CT`

```python
CHART_CT = 'application/vnd.openxmlformats-officedocument.drawingml.chart+xml'
```

## `CHART_REL_TYPE`

```python
CHART_REL_TYPE = REL_NS + '/chart'
```

## `DRAWING_CT`

```python
DRAWING_CT = 'application/vnd.openxmlformats-officedocument.drawing+xml'
```

## `DRAWING_REL_TYPE`

```python
DRAWING_REL_TYPE = REL_NS + '/drawing'
```

## `IMAGE_REL_TYPE`

```python
IMAGE_REL_TYPE = REL_NS + '/image'
```

## `REL_NS`

```python
REL_NS = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
```

## `SHEET_DRAWING_NS`

```python
SHEET_DRAWING_NS = 'http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing'
```

## `plan_added_sheet_drawing`

```python
plan_added_sheet_drawing(workbook, ws, part_plan, names, rel_entries)
```

Charts/images on an ADDED sheet: the stock writer emitted
``<drawing r:id>`` into the fresh sheet payload with an empty rel
Target; supply the drawing/chart/media parts and fill the target.
Returns the updated sheet rel entries.

## `plan_drawing_append`

```python
plan_drawing_append(workbook, ws, part_plan, names, drawing_part, original, existing_rels, charts, images)
```

Charts/images on a LOADED sheet whose drawing already exists:
new anchors appended INTO the original drawing bytes (``original`` may
already carry this save's shift patches) — only when that drawing is
anchor-only. Returns the new drawing payload.

## `plan_fresh_drawing`

```python
plan_fresh_drawing(workbook, ws, part_plan, names, sheet_part, original_sheet_rels, charts, images)
```

Charts/images on a LOADED sheet with no drawing machinery:
a fresh drawing part via the engine plus ONE spliced element. Returns
the ``<drawing r:id>`` bytes for the region splice.
