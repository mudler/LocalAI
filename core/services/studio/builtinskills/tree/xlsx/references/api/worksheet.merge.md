<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.merge`

## `MergeCell`

```python
MergeCell(ref = None)
```

Bases: `CellRange`

### `ref`

```python
ref = CellRange.coord
```

### `tagname`

```python
tagname = 'mergeCell'
```

## `MergeCells`

```python
MergeCells(count = None, mergeCell = ())
```

Bases: `Serialisable`

### `count`

```python
count
```

### `mergeCell`

```python
mergeCell = mergeCell
```

### `tagname`

```python
tagname = 'mergeCells'
```

## `MergedCellRange`

```python
MergedCellRange(worksheet, coord)
```

Bases: `CellRange`

MergedCellRange stores the border information of a merged cell in the top
left cell of the merged cell.
The remaining cells in the merged cell are stored as MergedCell objects and
get their border information from the upper left cell.

### `format`

```python
format()
```

Each cell of the merged cell is created as MergedCell if it does not
already exist.

The MergedCells at the edge of the merged cell gets its borders from
the upper left cell.

 - The top MergedCells get the top border from the top left cell.
 - The bottom MergedCells get the bottom border from the top left cell.
 - The left MergedCells get the left border from the top left cell.
 - The right MergedCells get the right border from the top left cell.

### `start_cell`

```python
start_cell = None
```

### `ws`

```python
ws = worksheet
```
