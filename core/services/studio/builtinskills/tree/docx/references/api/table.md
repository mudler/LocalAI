<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.table`

The `Table` object and related proxy classes.

## `Table`

```python
Table(tbl: CT_Tbl, parent: t.ProvidesStoryPart)
```

Bases: `StoryChild`

Proxy class for a WordprocessingML ``<w:tbl>`` element.

### `add_column`

```python
add_column(width: Length)
```

Return a `_Column` object of `width`, newly added rightmost to the table.

### `add_row`

```python
add_row()
```

Return a `_Row` instance, newly added bottom-most to the table.

### `alignment`

```python
alignment: WD_TABLE_ALIGNMENT | None
```

Read/write.

A member of `WdRowAlignment` or None, specifying the positioning of this
table between the page margins. `None` if no setting is specified, causing the
effective value to be inherited from the style hierarchy.

### `autofit`

```python
autofit: bool
```

`True` if column widths can be automatically adjusted to improve the fit of
cell contents.

`False` if table layout is fixed. Column widths are adjusted in either case if
total column width exceeds page width. Read/write boolean.

### `cell`

```python
cell(row_idx: int, col_idx: int) -> _Cell
```

`_Cell` at `row_idx`, `col_idx` intersection.

(0, 0) is the top, left-most cell.

### `column_cells`

```python
column_cells(column_idx: int) -> list[_Cell]
```

Sequence of cells in the column at `column_idx` in this table.

### `columns`

```python
columns()
```

`_Columns` instance representing the sequence of columns in this table.

### `row_cells`

```python
row_cells(row_idx: int) -> list[_Cell]
```

DEPRECATED: Use `table.rows[row_idx].cells` instead.

Sequence of cells in the row at `row_idx` in this table.

### `rows`

```python
rows() -> _Rows
```

`_Rows` instance containing the sequence of rows in this table.

### `style`

```python
style: _TableStyle | None
```

`_TableStyle` object representing the style applied to this table.

Read/write. The default table style for the document (often `Normal Table`) is
returned if the table has no directly-applied style. Assigning `None` to this
property removes any directly-applied table style causing it to inherit the
default table style of the document.

Note that the style name of a table style differs slightly from that displayed
in the user interface; a hyphen, if it appears, must be removed. For example,
`Light Shading - Accent 1` becomes `Light Shading Accent 1`.

### `table`

```python
table
```

Provide child objects with reference to the `Table` object they belong to,
without them having to know their direct parent is a `Table` object.

This is the terminus of a series of `parent._table` calls from an arbitrary
child through its ancestors.

### `table_direction`

```python
table_direction: WD_TABLE_DIRECTION | None
```

Member of `WdTableDirection` indicating cell-ordering direction.

For example: `WD_TABLE_DIRECTION.LTR`. `None` indicates the value is inherited
from the style hierarchy.

## `TableParent`

```python
TableParent: TypeAlias = 'Table | _Columns | _Rows'
```
