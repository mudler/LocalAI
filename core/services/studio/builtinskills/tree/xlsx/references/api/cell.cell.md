<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.cell.cell`

Manage individual cells in a spreadsheet.

## `Cell`

```python
Cell(worksheet, row = None, column = None, value = None, style_array = None)
```

Bases: `StyleableObject`

Describes cell associated properties.

Properties of interest include style, type, value, and address.

### `base_date`

```python
base_date
```

### `check_error`

```python
check_error(value)
```

Tries to convert Error" else N/A

### `check_string`

```python
check_string(value)
```

Check string coding, length, and line break character

### `col_idx`

```python
col_idx
```

The numerical index of the column

### `column`

```python
column = column
```

Column number of this cell (1-based)

### `column_letter`

```python
column_letter
```

### `comment`

```python
comment
```

Returns the comment associated with this cell

:type: `openpyxl.comments.Comment`

### `coordinate`

```python
coordinate
```

This cell's coordinate (ex. 'A5')

### `data_type`

```python
data_type
```

### `encoding`

```python
encoding
```

### `hyperlink`

```python
hyperlink
```

Return the hyperlink target or an empty string

### `internal_value`

```python
internal_value
```

Always returns the value for excel.

### `is_date`

```python
is_date
```

True if the value is formatted as a date

:type: bool

### `offset`

```python
offset(row = 0, column = 0)
```

Returns a cell location relative to this cell.

**Parameters:**

- **row** (`int`) – number of rows to offset
- **column** (`int`) – number of columns to offset

### `row`

```python
row = row
```

Row number of this cell (1-based)

### `value`

```python
value
```

Get or set the value held in the cell.

## `ERROR_CODES`

```python
ERROR_CODES = ('#NULL!', '#DIV/0!', '#VALUE!', '#REF!', '#NAME?', '#NUM!', '#N/A')
```

## `ILLEGAL_CHARACTERS_RE`

```python
ILLEGAL_CHARACTERS_RE = re.compile('[\\000-\\010]|[\\013-\\014]|[\\016-\\037]')
```

## `KNOWN_TYPES`

```python
KNOWN_TYPES = NUMERIC_TYPES + TIME_TYPES + STRING_TYPES + (bool, type(None))
```

## `MergedCell`

```python
MergedCell(worksheet, row = None, column = None)
```

Bases: `StyleableObject`

Describes the properties of a cell in a merged cell and helps to
display the borders of the merged cell.

The value of a MergedCell is always None.

### `column`

```python
column = column
```

### `comment`

```python
comment = None
```

### `coordinate`

```python
coordinate = Cell.coordinate
```

### `data_type`

```python
data_type = 'n'
```

### `hyperlink`

```python
hyperlink = None
```

### `row`

```python
row = row
```

### `value`

```python
value = _value
```

## `STRING_TYPES`

```python
STRING_TYPES = (str, bytes, CellRichText)
```

## `TIME_FORMATS`

```python
TIME_FORMATS = {datetime.datetime: numbers.FORMAT_DATE_DATETIME, datetime.date: numbers.FORMAT_DATE_YYYYMMDD2, datetime.time: numbers.FORMAT_DATE_TIME6, datetime.timedelta: numbers.FORMAT_DATE_TIMEDELTA}
```

## `TIME_TYPES`

```python
TIME_TYPES = (datetime.datetime, datetime.date, datetime.time, datetime.timedelta)
```

## `TYPE_BOOL`

```python
TYPE_BOOL = 'b'
```

## `TYPE_ERROR`

```python
TYPE_ERROR = 'e'
```

## `TYPE_FORMULA`

```python
TYPE_FORMULA = 'f'
```

## `TYPE_FORMULA_CACHE_STRING`

```python
TYPE_FORMULA_CACHE_STRING = 'str'
```

## `TYPE_INLINE`

```python
TYPE_INLINE = 'inlineStr'
```

## `TYPE_NULL`

```python
TYPE_NULL = 'n'
```

## `TYPE_NUMERIC`

```python
TYPE_NUMERIC = 'n'
```

## `TYPE_STRING`

```python
TYPE_STRING = 's'
```

## `VALID_TYPES`

```python
VALID_TYPES = (TYPE_STRING, TYPE_FORMULA, TYPE_NUMERIC, TYPE_BOOL, TYPE_NULL, TYPE_INLINE, TYPE_ERROR, TYPE_FORMULA_CACHE_STRING)
```

## `WriteOnlyCell`

```python
WriteOnlyCell(ws = None, value = None)
```

## `get_time_format`

```python
get_time_format(t)
```

## `get_type`

```python
get_type(t, value)
```
