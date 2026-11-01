<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.package.cells`

``diff_cells(a, b)``: which cells changed between two workbooks, as

## `CellsDiff`

```python
CellsDiff(changes, sheets_added, sheets_removed)
```

### `SCHEMA`

```python
SCHEMA = 'cells_diff'
```

### `VERSION`

```python
VERSION = 1
```

### `changes`

```python
changes = changes
```

### `clean`

```python
clean
```

### `sheets_added`

```python
sheets_added = sheets_added
```

### `sheets_removed`

```python
sheets_removed = sheets_removed
```

### `to_dict`

```python
to_dict()
```

## `diff_cells`

```python
diff_cells(a, b)
```

Cell-level semantic diff of two packages (paths, bytes, or binary
file-likes). Deterministic order: sheet, then row, then column.
