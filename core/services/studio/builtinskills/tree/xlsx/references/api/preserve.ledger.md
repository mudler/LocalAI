<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.ledger`

Records what the object model changed, so the splice save can apply

## `DirtyLedger`

```python
DirtyLedger()
```

### `added_sheets`

```python
added_sheets = set()
```

### `arm`

```python
arm(wb, rich_text = False)
```

### `armed`

```python
armed = False
```

### `cache_writes`

```python
cache_writes = {}
```

### `cells`

```python
cells = {}
```

### `chartsheet_snapshots`

```python
chartsheet_snapshots = {}
```

### `check_style_registry`

```python
check_style_registry(wb)
```

Refuse if any interned style component that existed at arm time
was mutated in place (the StyleProxy nested-object leak): such a
mutation silently restyles every aliased cell and cannot be
expressed as an append-only styles.xml edit.

### `comment_snapshots`

```python
comment_snapshots = {}
```

### `core_snapshot`

```python
core_snapshot = None
```

### `custom_snapshot`

```python
custom_snapshot = None
```

### `dirty_coordinates`

```python
dirty_coordinates(ws)
```

### `dxfs_len`

```python
dxfs_len = 0
```

### `external_links_snapshot`

```python
external_links_snapshot = ()
```

### `formulas_changed`

```python
formulas_changed = False
```

### `image_replacements`

```python
image_replacements = {}
```

### `is_loaded_sheet`

```python
is_loaded_sheet(ws)
```

### `loaded_sheet_titles`

```python
loaded_sheet_titles = frozenset()
```

### `mark_cell`

```python
mark_cell(ws, row, column)
```

### `named_styles_len`

```python
named_styles_len = 0
```

### `object_snapshots`

```python
object_snapshots = {}
```

### `orig_cell_styles_len`

```python
orig_cell_styles_len = 0
```

### `pinned_regions`

```python
pinned_regions = {}
```

### `pivot_refresh_requests`

```python
pivot_refresh_requests = set()
```

### `pivot_source_snapshots`

```python
pivot_source_snapshots = {}
```

### `protection_warned`

```python
protection_warned = set()
```

### `region_snapshots`

```python
region_snapshots = {}
```

### `removed_sheets`

```python
removed_sheets = []
```

### `renames`

```python
renames = {}
```

### `rich_text_mode`

```python
rich_text_mode = False
```

### `row_attr_snapshots`

```python
row_attr_snapshots = {}
```

### `sheet_order`

```python
sheet_order = []
```

### `sheet_states`

```python
sheet_states = {}
```

### `shifts`

```python
shifts = {}
```

### `template_flag`

```python
template_flag = False
```

### `value_overwrites`

```python
value_overwrites = {}
```

### `workbook_snapshot`

```python
workbook_snapshot = None
```

## `allow_sheet_removal`

```python
allow_sheet_removal(wb, ws)
```

Removing a sheet ADDED in this session is a net no-op and allowed;
removing a loaded sheet is refused (returns False).

## `audit_sheet_removal`

```python
audit_sheet_removal(wb, ws)
```

The reference audit before a LOADED sheet may be removed: anything on ANOTHER sheet pointing at the victim
refuses with the full enumeration — formulas (3-D endpoints and
textual/INDIRECT included), defined names, chart parts, pivot parts.

## `begin_move_range`

```python
begin_move_range(ws, move_spec)
```

move_range under preserve: expressed as tracked
cell edits (source cleared + destination written — no rows shift, so
no byte renumber). Guards refuse what the move cannot keep coherent:
merges/CF/DV/tables intersecting either rectangle, and formulas
OUTSIDE the moved block referencing the source (Excel's cut-paste
would follow them; we do not rewrite them in this wave).

## `begin_structural_edit`

```python
begin_structural_edit(ws, operation, index, amount)
```

Gate for insert/delete rows/cols under preserve: shifts
on fully-modeled sheets PROCEED (reference rewriting + byte renumber);
anything with unmodeled range-bearing content refuses with the precise
blocker and victim list. Returns True when the caller must run the
model fixups after mutating.

## `check_protection`

```python
check_protection(cell)
```

Protection awareness: we report protection, we
never enforce or bypass it. Called BEFORE the value binds (a strict
refusal must be atomic): a write to a locked cell of a protected
sheet warns once per sheet — or refuses under wb.strict_protection.
Scope: value writes (the chokepoint agents hit); style/comment edits
to locked cells are not protection-checked.

## `diff_objects`

```python
diff_objects(ws, armed)
```

(kind, key) pairs whose settled serialization drifted since arm —
in-session mutations of preserved-part-backed objects.

## `finish_structural_edit`

```python
finish_structural_edit(ws, operation, index, amount, transaction = None)
```

Model-side reference fixups + snapshot rebasing, after the cells
moved (see structural.apply_model_shift). Returns the pinned
AddressRemap: pre-edit addresses must be remapped
through it, never reused.

## `mark_cell_dirty`

```python
mark_cell_dirty(cell, formula_involved = False, value_change = False)
```

Called from Cell mutation chokepoints (value bind, style set,
hyperlink/comment/data_type assignment). ``value_change`` marks the
coordinate as a VALUE overwrite — the only case where a cell's cm/vm
rich-value metadata may drop (style-only re-emissions
and dissolution re-emits must carry it).

## `mark_deleted_cell`

```python
mark_deleted_cell(ws, row, column, was_formula)
```

## `mark_sheet_added`

```python
mark_sheet_added(wb, ws)
```

## `mark_styleable_dirty`

```python
mark_styleable_dirty(instance)
```

Called from the style descriptors; ``instance`` is a Cell or a
row/column dimension (both carry ``.parent`` = worksheet).

## `record_rename`

```python
record_rename(sheet_child, new_title)
```

Renaming a LOADED sheet: the cascade
rewrite. Model formulas and defined names are rewritten NOW (upstream
rewrites nothing — the model must stay coherent in-session); chart
parts referencing the old name are byte-patched at save; the sheets
entry in workbook.xml gets a name patch keyed by the ORIGINAL title.
Refuses (atomic, before any mutation): textual references the rewrite
cannot see (the old title inside formula STRING literals — the
INDIRECT class), and pivot parts referencing the old name.

## `record_sheet_removal`

```python
record_sheet_removal(wb, ws)
```

Called by Workbook.remove for a LOADED sheet, AFTER the audit.

## `refuse_chart_or_image_add`

```python
refuse_chart_or_image_add(ws, what)
```

Call-time gate for add_chart/add_image under preserve: additions are supported on added sheets, on loaded
sheets without drawing machinery (fresh drawing part + one spliced
element), and on loaded sheets whose existing drawing is anchor-only
(anchors appended into the original part). A drawing carrying anything
else refuses NOW — atomically, before the object joins the model.

## `refuse_sheet_lifecycle`

```python
refuse_sheet_lifecycle(wb, operation, detail)
```

## `refuse_structural_edit`

```python
refuse_structural_edit(ws, operation, index = None)
```

Row/column shifts under preserve are refused in v0, with the precise
list of what the shift would strand: formulas (cross-
sheet included), defined names, CF/DV ranges, merges, tables, and
series ranges inside preserved chart bytes. Raised BEFORE any mutation,
and only when reference rewriting cannot turn the shift into a correct
edit.

## `render_core_model`

```python
render_core_model(wb)
```

## `render_custom_model`

```python
render_custom_model(wb)
```
