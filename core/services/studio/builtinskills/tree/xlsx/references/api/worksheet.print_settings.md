<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.print_settings`

## `COL_RANGE`

```python
COL_RANGE = '(?P<cols>[$]?(?P<min_col>[a-zA-Z]{1,3}):[$]?(?P<max_col>[a-zA-Z]{1,3}))'
```

## `COL_RANGE_RE`

```python
COL_RANGE_RE = re.compile(COL_RANGE)
```

## `ColRange`

```python
ColRange(range_string = None, min_col = None, max_col = None)
```

Bases: `Strict`

Represent a range of at least one column

### `max_col`

```python
max_col = max_col
```

### `min_col`

```python
min_col = min_col
```

## `PRINT_AREA_RE`

```python
PRINT_AREA_RE = re.compile(f'({SHEET_TITLE})?(?P<cells>{RANGE_EXPR})', re.VERBOSE)
```

## `PrintArea`

```python
PrintArea(ranges = (), title = '')
```

Bases: `MultiCellRange`

### `from_string`

```python
from_string(value)
```

### `title`

```python
title = ''
```

## `PrintTitles`

```python
PrintTitles(cols = None, rows = None, title = '')
```

Bases: `Strict`

Contains at least either a range of rows or columns

### `cols`

```python
cols = cols
```

### `from_string`

```python
from_string(value)
```

### `rows`

```python
rows = rows
```

### `title`

```python
title = title
```

## `ROW_RANGE`

```python
ROW_RANGE = '(?P<rows>[$]?(?P<min_row>\\d+):[$]?(?P<max_row>\\d+))'
```

## `ROW_RANGE_RE`

```python
ROW_RANGE_RE = re.compile(ROW_RANGE)
```

## `RowRange`

```python
RowRange(range_string = None, min_row = None, max_row = None)
```

Bases: `Strict`

Represent a range of at least one row

### `max_row`

```python
max_row = max_row
```

### `min_row`

```python
min_row = min_row
```

## `TITLES_REGEX`

```python
TITLES_REGEX = re.compile('{0}{1}?,?{2}?,?'.format(SHEET_TITLE, ROW_RANGE, COL_RANGE), re.VERBOSE)
```
