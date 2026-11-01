<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.crosspart`

Targeted edits to non-worksheet parts under preserve mode.

## `CT_STYLESHEET_INDEX`

```python
CT_STYLESHEET_INDEX = {tag: i for i, tag in enumerate(CT_STYLESHEET_ORDER)}
```

## `CT_STYLESHEET_ORDER`

```python
CT_STYLESHEET_ORDER = ['numFmts', 'fonts', 'fills', 'borders', 'cellStyleXfs', 'cellXfs', 'cellStyles', 'dxfs', 'tableStyles', 'colors', 'extLst']
```

## `CT_WORKBOOK_INDEX`

```python
CT_WORKBOOK_INDEX = {tag: i for i, tag in enumerate(CT_WORKBOOK_ORDER)}
```

## `CT_WORKBOOK_ORDER`

```python
CT_WORKBOOK_ORDER = ['fileVersion', 'fileSharing', 'workbookPr', 'workbookProtection', 'bookViews', 'sheets', 'functionGroups', 'externalReferences', 'definedNames', 'calcPr', 'oleSize', 'customWorkbookViews', 'pivotCaches', 'smartTagPr', 'smartTagTypes', 'webPublishing', 'fileRecoveryPr', 'webPublishObjects', 'extLst']
```

## `Node`

```python
Node(name, start)
```

### `attrs`

```python
attrs = {}
```

### `children`

```python
children = []
```

### `content_end`

```python
content_end = None
```

### `content_start`

```python
content_start = None
```

### `end`

```python
end = None
```

### `local`

```python
local()
```

### `name`

```python
name = name
```

### `self_closing`

```python
self_closing = False
```

### `start`

```python
start = start
```

## `WB_SPLICEABLE`

```python
WB_SPLICEABLE = {'definedNames', 'calcPr', 'bookViews'}
```

## `apply_edits`

```python
apply_edits(data, edits)
```

Apply sorted, non-overlapping (start, end, replacement) edits.

## `ct_append_defaults`

```python
ct_append_defaults(data, defaults)
```

Append Default entries; ``defaults`` = [(extension, content_type)].
Callers check for existing extensions first (duplicates are illegal).

## `ct_append_overrides`

```python
ct_append_overrides(data, overrides)
```

Append Override entries; ``overrides`` = [(part_name, content_type)].

## `ct_remove_override`

```python
ct_remove_override(data, part_name)
```

Remove the Override for ``part_name``; no-op when absent.

## `plan_styles_xml`

```python
plan_styles_xml(wb, led, original, translator)
```

New styles.xml bytes appending the styles created since arming, or
None when nothing was added. Never rewrites existing entries.

Fonts/fills/borders/dxfs come from the MODEL tails (their numbering is
file-stable at load: seeded in file order, never renumbered). Cell xfs
and custom number formats come from the `StyleTranslator`, which
owns the model-to-file numbering translation.

## `plan_workbook_xml`

```python
plan_workbook_xml(wb, led, original, new_sheet_entries, force_tags = ())
```

New workbook.xml bytes, or None if nothing changes.

``new_sheet_entries``: [(title, sheet_id, rid, state)] for added sheets.
``force_tags``: spliceable elements re-rendered even when the arm-vs-save
diff sees no change (the recalc-on-load flag: the model defaults it, so
only a forced re-render can write it into files that lack it).
Raises for changes outside the v0-spliceable set.

## `rels_append`

```python
rels_append(data, entries)
```

Append relationship entries: [(rId, type, target, target_mode|None)].

## `rels_next_rid`

```python
rels_next_rid(data)
```

## `rels_remove_by_target_suffix`

```python
rels_remove_by_target_suffix(data, suffix)
```

Remove relationships whose Target ends with ``suffix``.

## `render_rels_document`

```python
render_rels_document(entries)
```

A whole new .rels part (for sheets that had none).

## `render_workbook_elements`

```python
render_workbook_elements(wb)
```

Per-element model renders of workbook.xml (self-consistent between
arm and save; 'sheets' is compared structurally, not by render).

## `scan_small`

```python
scan_small(data, expected_root, max_depth = 3, *, allow_prefixed_root = False)
```

Compact span scanner for small XML parts (workbook.xml, styles.xml,
[Content_Types].xml, rels). Records children down to ``max_depth``.
Refuses prefixed roots unless a caller has independently validated the
expanded root name and explicitly opts in.
