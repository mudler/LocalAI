<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.utils.cell`

Collection of utilities used within the package and also available for client code

## `ABSOLUTE_RE`

```python
ABSOLUTE_RE = re.compile('^' + RANGE_EXPR + '$', re.VERBOSE)
```

## `COL_RANGE`

```python
COL_RANGE = '[A-Z]{1,3}:[A-Z]{1,3}:'
```

## `COORD_RE`

```python
COORD_RE = re.compile('^[$]?([A-Za-z]{1,3})[$]?(\\d+)$')
```

## `RANGE_EXPR`

```python
RANGE_EXPR = '\n[$]?(?P<min_col>[A-Za-z]{1,3})?\n[$]?(?P<min_row>\\d+)?\n(:[$]?(?P<max_col>[A-Za-z]{1,3})?\n[$]?(?P<max_row>\\d+)?)?\n'
```

## `ROW_RANGE`

```python
ROW_RANGE = '\\d+:\\d+:'
```

## `SHEETRANGE_RE`

```python
SHEETRANGE_RE = re.compile('{0}(?P<cells>{1})(?=,?)'.format(SHEET_TITLE, RANGE_EXPR), re.VERBOSE)
```

## `SHEET_TITLE`

```python
SHEET_TITLE = "\n(('(?P<quoted>([^']|'')*)')|(?P<notquoted>[^'^ ^!]*))!"
```

## `absolute_coordinate`

```python
absolute_coordinate(coord_string)
```

Convert a coordinate to an absolute coordinate string (B12 -> $B$12)

## `cols_from_range`

```python
cols_from_range(range_string)
```

Get individual addresses for every cell in a range.
Yields one row at a time.

## `column_index_from_string`

```python
column_index_from_string(col)
```

Convert ASCII column name (base 26) to decimal with 1-based index

Characters represent descending multiples of powers of 26

"AFZ" == 26 * pow(26, 0) + 6 * pow(26, 1) + 1 * pow(26, 2)

## `coordinate_from_string`

```python
coordinate_from_string(coord_string)
```

Convert a coordinate string like 'B12' to a tuple ('B', 12)

## `coordinate_to_tuple`

```python
coordinate_to_tuple(coordinate)
```

Convert an Excel style coordinate to (row, column) tuple

## `get_column_interval`

```python
get_column_interval(start, end)
```

Given the start and end columns, return all the columns in the series.

The start and end columns can be either column letters or 1-based
indexes.

## `get_column_letter`

```python
get_column_letter(col_idx)
```

Convert decimal column position to its ASCII (base 26) form.

Because column indices are 1-based, strides are actually pow(26, n) + 26
Hence, a correction is applied between pow(26, n) and pow(26, 2) + 26 to
prevent and additional column letter being prepended

"A" == 1 == pow(26, 0)
"Z" == 26 == pow(26, 0) + 26 // decimal equivalent 10
"AA" == 27 == pow(26, 1) + 1
"ZZ" == 702 == pow(26, 2) + 26 // decimal equivalent 100

## `quote_sheetname`

```python
quote_sheetname(sheetname)
```

Add quotes around sheetnames if they contain spaces.

## `range_boundaries`

```python
range_boundaries(range_string)
```

Convert a range string into a tuple of boundaries:
(min_col, min_row, max_col, max_row)
Cell coordinates will be converted into a range with the cell at both end

## `range_to_tuple`

```python
range_to_tuple(range_string)
```

Convert a worksheet range to the sheetname and maximum and minimum
coordinate indices

## `rows_from_range`

```python
rows_from_range(range_string)
```

Get individual addresses for every cell in a range.
Yields one row at a time.
