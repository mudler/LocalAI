<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.cell_range`

## `CellRange`

```python
CellRange(range_string = None, min_col = None, min_row = None, max_col = None, max_row = None, title = None)
```

Bases: `Serialisable`

Represents a range in a sheet: title and coordinates.

This object is used to perform operations on ranges, like:

- shift, expand or shrink
- union/intersection with another sheet range,

We can check whether a range is:

- equal or not equal to another,
- disjoint of another,
- contained in another.

We can get:

- the size of a range.
- the range bounds (vertices)
- the coordinates,
- the string representation,

### `bottom`

```python
bottom
```

A list of cell coordinates that comprise the bottom of the range

### `bounds`

```python
bounds
```

Vertices of the range as a tuple

### `cells`

```python
cells
```

### `cols`

```python
cols
```

Return cell coordinates as columns

### `coord`

```python
coord
```

Excel-style representation of the range

### `expand`

```python
expand(right = 0, down = 0, left = 0, up = 0)
```

Expand the range by the dimensions provided.

**Parameters:**

- **right** (`int`) – expand range to the right by this number of cells
- **down** (`int`) – expand range down by this number of cells
- **left** (`int`) – expand range to the left by this number of cells
- **up** (`int`) – expand range up by this number of cells

### `intersection`

```python
intersection(other)
```

Return a new range with cells common to this range and *other*

**Parameters:**

- **other** (`CellRange`) – Other sheet range.

**Returns:**

-  – the intersecting sheet range.

### `isdisjoint`

```python
isdisjoint(other)
```

Return ``True`` if this range has no cell in common with *other*.
Ranges are disjoint if and only if their intersection is the empty range.

**Parameters:**

- **other** (`CellRange`) – Other sheet range.

**Returns:**

-  – ``True`` if the range has no cells in common with other.

### `issubset`

```python
issubset(other)
```

Test whether every cell in this range is also in *other*.

**Parameters:**

- **other** (`CellRange`) – Other sheet range

**Returns:**

-  – ``True`` if *range* <= *other*.

### `issuperset`

```python
issuperset(other)
```

Test whether every cell in *other* is in this range.

**Parameters:**

- **other** (`CellRange`) – Other sheet range

**Returns:**

-  – ``True`` if *range* >= *other* (or *other* in *range*).

### `left`

```python
left
```

A list of cell coordinates that comprise the left-side of the range

### `max_col`

```python
max_col = max_col
```

### `max_row`

```python
max_row = max_row
```

### `min_col`

```python
min_col = min_col
```

### `min_row`

```python
min_row = min_row
```

### `right`

```python
right
```

A list of cell coordinates that comprise the right-side of the range

### `rows`

```python
rows
```

Return cell coordinates as rows

### `shift`

```python
shift(col_shift = 0, row_shift = 0)
```

Shift the focus of the range according to the shift values (*col_shift*, *row_shift*).

**Parameters:**

- **col_shift** (`int`) – number of columns to be moved by, can be negative
- **row_shift** (`int`) – number of rows to be moved by, can be negative

### `shrink`

```python
shrink(right = 0, bottom = 0, left = 0, top = 0)
```

Shrink the range by the dimensions provided.

**Parameters:**

- **right** (`int`) – shrink range from the right by this number of cells
- **down** (`int`) – shrink range from the top by this number of cells
- **left** (`int`) – shrink range from the left by this number of cells
- **up** (`int`) – shrink range from the bottom by this number of cells

### `size`

```python
size
```

Return the size of the range as a dictionary of rows and columns.

### `title`

```python
title = title
```

### `top`

```python
top
```

A list of cell coordinates that comprise the top of the range

### `union`

```python
union(other)
```

Return the minimal superset of this range and *other*. This new range
will contain all cells from this range, *other*, and any additional
cells required to form a rectangular ``CellRange``.

**Parameters:**

- **other** (`CellRange`) – Other sheet range.

**Returns:**

-  – a ``CellRange`` that is a superset of this and *other*.

## `MultiCellRange`

```python
MultiCellRange(ranges = set())
```

Bases: `Strict`

### `add`

```python
add(coord)
```

Add a cell coordinate or CellRange

### `ranges`

```python
ranges = set(ranges)
```

### `remove`

```python
remove(coord)
```

### `sorted`

```python
sorted()
```

Return a sorted list of items
