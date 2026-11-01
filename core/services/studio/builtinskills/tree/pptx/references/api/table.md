<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.table`

Table-related objects such as Table and Cell.

## `Table`

```python
Table(tbl: CT_Table, graphic_frame: GraphicFrame)
```

Bases: `object`

A DrawingML table object.

Not intended to be constructed directly, use
`.Slide.shapes.add_table` to add a table to a slide.

### `cell`

```python
cell(row_idx: int, col_idx: int) -> _Cell
```

Return cell at `row_idx`, `col_idx`.

Return value is an instance of `_Cell`. `row_idx` and `col_idx` are zero-based, e.g.
cell(0, 0) is the top, left cell in the table.

### `columns`

```python
columns() -> _ColumnCollection
```

`_ColumnCollection` instance for this table.

Provides access to `_Column` objects representing the table's columns. `_Column` objects
are accessed using list notation, e.g. `col = tbl.columns[0]`.

### `delete_column`

```python
delete_column(col_idx: int) -> None
```

Remove the column at 0-based `col_idx`, cells included.

paper-pptx addition. The `a:gridCol` and every row's cell at that
grid position are removed together, so each row keeps exactly one `a:tc` per grid
column, and the graphic frame's width is recalculated from the remaining columns.

Merged-cell guard is cell-wise: the operation refuses
(`UnsupportedStructureError`, tree untouched) only when a *horizontal* merge extends
beyond the deleted column - a vertical merge lying wholly inside the column is
deleted with it. Deleting the last remaining column raises `ValueError` (delete the
table's shape instead).

### `delete_row`

```python
delete_row(row_idx: int) -> None
```

Remove the row at 0-based `row_idx`, cells included.

paper-pptx addition. The graphic frame's height is recalculated
from the remaining rows.

Merged-cell guard is cell-wise: the operation refuses
(`UnsupportedStructureError`, tree untouched) only when a *vertical* merge extends
beyond the deleted row - a horizontal merge lying wholly inside the row (e.g. a
merged header) is deleted with it and must not poison other rows' operations.
Deleting the last remaining row raises `ValueError`.

### `first_col`

```python
first_col: bool
```

When `True`, indicates first column should have distinct formatting.

Read/write. Distinct formatting is used, for example, when the first column contains row
headings (is a side-heading column).

### `first_row`

```python
first_row: bool
```

When `True`, indicates first row should have distinct formatting.

Read/write. Distinct formatting is used, for example, when the first row contains column
headings.

### `horz_banding`

```python
horz_banding: bool
```

When `True`, indicates rows should have alternating shading.

Read/write. Used to allow rows to be traversed more easily without losing track of which
row is being read.

### `insert_column`

```python
insert_column(after: int, *, width: Length | None = None, copy_format_from: int | None = None) -> _Column
```

Insert a new empty column immediately after 0-based column `after`; return it.

paper-pptx addition. `after=-1` inserts before the first column.
`copy_format_from` is the zero-based index of a column in the table before insertion.
When provided, each new cell receives a deep copy of that row's template-cell `a:tcPr`
(direct formatting only); text and merge state are never copied. `width` (EMU int) wins
when provided, otherwise the template column supplies the width. With no template, width
defaults to the neighboring column at `after` (the first column when `after=-1`). A new
minimal empty cell is inserted at the same grid position in every row - the grid stays
consistent by construction - and the graphic frame's width is recalculated.

Merged-cell guard is cell-wise: refuses (`UnsupportedStructureError`, tree
untouched) only when the insertion boundary would split a horizontal merge; vertical
merges elsewhere in the table never block the operation.

### `insert_row`

```python
insert_row(after: int, *, copy_format_from: int | None = None) -> _Row
```

Insert a new empty row immediately after 0-based row `after`; return it.

paper-pptx addition. `after=-1` inserts before the first row. The
new row holds one minimal empty cell per grid column. Row height and per-cell
formatting (each cell's `a:tcPr`: fill, margins, anchor) are copied from the row at
`copy_format_from` when given, otherwise the height of the neighboring row at
`after` (the first row when `after=-1`) is used and cells carry default formatting.
Merge attributes are never copied - the new row is always unmerged. Text is never
copied. The graphic frame's height is recalculated.

Merged-cell guard is cell-wise: refuses (`UnsupportedStructureError`, tree
untouched) only when the insertion boundary would split a vertical merge; a merged
header row never blocks body-row insertion.

### `iter_cells`

```python
iter_cells() -> Iterator[_Cell]
```

Generate _Cell object for each cell in this table.

Each grid cell is generated in left-to-right, top-to-bottom order.

### `last_col`

```python
last_col: bool
```

When `True`, indicates the rightmost column should have distinct formatting.

Read/write. Used, for example, when a row totals column appears at the far right of the
table.

### `last_row`

```python
last_row: bool
```

When `True`, indicates the bottom row should have distinct formatting.

Read/write. Used, for example, when a totals row appears as the bottom row.

### `notify_height_changed`

```python
notify_height_changed() -> None
```

Called by a row when its height changes.

Triggers the graphic frame to recalculate its total height (as the sum of the row
heights).

### `notify_width_changed`

```python
notify_width_changed() -> None
```

Called by a column when its width changes.

Triggers the graphic frame to recalculate its total width (as the sum of the column
widths).

### `part`

```python
part: BaseSlidePart
```

The package part containing this table.

### `rows`

```python
rows()
```

`_RowCollection` instance for this table.

Provides access to `_Row` objects representing the table's rows. `_Row` objects are
accessed using list notation, e.g. `col = tbl.rows[0]`.

### `vert_banding`

```python
vert_banding: bool
```

When `True`, indicates columns should have alternating shading.

Read/write. Used to allow columns to be traversed more easily without losing track of
which column is being read.
