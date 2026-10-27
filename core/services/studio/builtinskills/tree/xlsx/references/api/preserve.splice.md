<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.splice`

Apply ledger-recorded edits to one original worksheet part, byte-wise.

## `SpliceRefusal`

Bases: `UnsupportedStructureError`

## `resolve_dirty_cells`

```python
resolve_dirty_cells(ws, ledger_dirty, scan, value_overwrites = frozenset())
```

The effective dirty-coordinate set for one sheet.

- rich-text cells are always dirty (in-place edits bypass every hook);
- a dirty cell intersecting a shared-formula group dissolves the WHOLE
  group: every member re-emits as a plain formula from the model (the
  model already holds the expanded formulas);
- a dirty cell intersecting an array formula refuses;
- a dirty cell carrying cm/vm metadata or unexpected children refuses.

## `splice_sheet`

```python
splice_sheet(ws, original, dirty_cells, region_changes, row_attr_changes, scan = None, cf_replacement = None, hyperlinks_replacement = None, style_resolver = None, value_overwrites = frozenset(), cache_writes = None, cache_invalidations = None)
```

Return the new part payload for one worksheet.

``dirty_cells``: resolved coordinate set (see resolve_dirty_cells).
``region_changes``: `{tag: serialized bytes or None}` — user-changed
satellite regions (None = region now absent).
``row_attr_changes``: `{row_index: {attr: value}`} — changed row display
attributes.
``scan``: a SheetScan of ``original`` if the caller already has one.
``cf_replacement``: bytes replacing ALL conditionalFormatting elements
(gated by the caller; may be b"" to remove them).
``hyperlinks_replacement``: bytes replacing the hyperlinks element.
``style_resolver``: cell -> FILE xf index (StyleTranslator) —
model style indices must never reach the spliced bytes.
``cache_writes``: `{(row, col): computed_value}` — cached-value updates
for untouched formula cells in a preserved recalc candidate: the
<f> bytes stay verbatim, only the cached <v> (and its t attribute)
change.
``cache_invalidations``: `{(row, col)}` — formula cells and array followers
whose cached <v> must be removed because results are uncertified.
