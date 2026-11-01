<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.worksheet`

Worksheet is the 2nd-level container in Excel.

## `Worksheet`

```python
Worksheet(parent, title = None)
```

Bases: `_WorkbookChild`

Represents a worksheet.

Do not create worksheets yourself,
use `openpyxl.workbook.Workbook.create_sheet` instead

### `BREAK_COLUMN`

```python
BREAK_COLUMN = 2
```

### `BREAK_NONE`

```python
BREAK_NONE = 0
```

### `BREAK_ROW`

```python
BREAK_ROW = 1
```

### `ORIENTATION_LANDSCAPE`

```python
ORIENTATION_LANDSCAPE = 'landscape'
```

### `ORIENTATION_PORTRAIT`

```python
ORIENTATION_PORTRAIT = 'portrait'
```

### `PAPERSIZE_A3`

```python
PAPERSIZE_A3 = '8'
```

### `PAPERSIZE_A4`

```python
PAPERSIZE_A4 = '9'
```

### `PAPERSIZE_A4_SMALL`

```python
PAPERSIZE_A4_SMALL = '10'
```

### `PAPERSIZE_A5`

```python
PAPERSIZE_A5 = '11'
```

### `PAPERSIZE_EXECUTIVE`

```python
PAPERSIZE_EXECUTIVE = '7'
```

### `PAPERSIZE_LEDGER`

```python
PAPERSIZE_LEDGER = '4'
```

### `PAPERSIZE_LEGAL`

```python
PAPERSIZE_LEGAL = '5'
```

### `PAPERSIZE_LETTER`

```python
PAPERSIZE_LETTER = '1'
```

### `PAPERSIZE_LETTER_SMALL`

```python
PAPERSIZE_LETTER_SMALL = '2'
```

### `PAPERSIZE_STATEMENT`

```python
PAPERSIZE_STATEMENT = '6'
```

### `PAPERSIZE_TABLOID`

```python
PAPERSIZE_TABLOID = '3'
```

### `SHEETSTATE_HIDDEN`

```python
SHEETSTATE_HIDDEN = 'hidden'
```

### `SHEETSTATE_VERYHIDDEN`

```python
SHEETSTATE_VERYHIDDEN = 'veryHidden'
```

### `SHEETSTATE_VISIBLE`

```python
SHEETSTATE_VISIBLE = 'visible'
```

### `active_cell`

```python
active_cell
```

### `add_chart`

```python
add_chart(chart, anchor = None)
```

Add a chart to the sheet
Optionally provide a cell for the top-left anchor

### `add_data_validation`

```python
add_data_validation(data_validation)
```

Add a data-validation object to the sheet. The data-validation
object defines the type of data-validation to be applied and the
cell or range of cells it should apply to.

### `add_image`

```python
add_image(img, anchor = None)
```

Add an image to the sheet.
Optionally provide a cell for the top-left anchor

### `add_pivot`

```python
add_pivot(pivot)
```

### `add_table`

```python
add_table(table)
```

Check for duplicate name in definedNames and other worksheet tables
before adding table.

### `allowed_values`

```python
allowed_values(cell)
```

The data-validation vocabulary for ``cell`` (address string or
Cell), or None when no list-type validation covers it
(paper-xlsx).

### `append`

```python
append(iterable)
```

### `append_table_row`

```python
append_table_row(table_name, values)
```

Append one row to a supported named table atomically.

**Parameters:**

- **table_name** (`str`) – Name of the table to expand.
- **values** (`iterable | mapping`) – Row values as a sequence or column-name mapping.

**Returns:**

- `None` – `None`.

### `array_formulae`

```python
array_formulae
```

Returns a dictionary of cells with array formulae and the cells in array

### `calculate_dimension`

```python
calculate_dimension()
```

Return the minimum bounding range for all cells containing data (ex. 'A1:M24')

:rtype: string

### `cell`

```python
cell(row, column, value = None)
```

Returns a cell object based on the given coordinates.

Usage: cell(row=15, column=1, value=5)

Calling `cell` creates cells in memory when they
are first accessed.

**Parameters:**

- **row** (`int`) – row index of the cell (e.g. 4)
- **column** (`int`) – column index of the cell (e.g. 3)
- **value** (`numeric, ``datetime.time``, string, bool, | none`) – value of the cell (e.g. 5)

### `column_groups`

```python
column_groups
```

Return a list of column ranges where more than one column

### `columns`

```python
columns
```

Produces all cells in the worksheet, by column (see `iter_cols`)

### `delete_cols`

```python
delete_cols(idx, amount = 1)
```

Delete column or columns from col==idx

Under ``preserve=True`` on a loaded sheet, references into the
shifted range are rewritten and an ``AddressRemap`` is returned;
a delete that would strand a reference refuses with
``UnsupportedStructureError`` and changes nothing. Stock loads and
added sheets keep upstream behaviour and return ``None``.

### `delete_rows`

```python
delete_rows(idx, amount = 1)
```

Delete row or rows from row==idx

Under ``preserve=True`` on a loaded sheet, references into the
shifted range are rewritten and an ``AddressRemap`` is returned;
a delete that would strand a reference (or drop cells a chart or
name still points at) refuses with ``UnsupportedStructureError``
and changes nothing. Stock loads and added sheets keep upstream
behaviour and return ``None``.

### `dimensions`

```python
dimensions
```

Returns the result of `calculate_dimension`

### `freeze_panes`

```python
freeze_panes
```

### `insert_cols`

```python
insert_cols(idx, amount = 1)
```

Insert column or columns before col==idx

Under ``preserve=True`` on a loaded sheet, references into the
shifted range are rewritten and an ``AddressRemap`` is returned;
a shift that would strand a reference refuses with
``UnsupportedStructureError`` and changes nothing. Stock loads and
added sheets keep upstream behaviour and return ``None``.

### `insert_rows`

```python
insert_rows(idx, amount = 1)
```

Insert row or rows before row==idx

Under ``preserve=True`` on a loaded sheet the fork rewrites every
reference that points into the shifted range (formulas, defined
names, chart series) and returns an ``AddressRemap`` so pre-edit
addresses can be remapped; a shift that would strand a reference it
cannot rewrite refuses with ``UnsupportedStructureError`` and
changes nothing. Stock loads and in-session-added sheets keep the
upstream behaviour — references are NOT updated — and return
``None``.

### `iter_cols`

```python
iter_cols(min_col = None, max_col = None, min_row = None, max_row = None, values_only = False)
```

Produces cells from the worksheet, by column. Specify the iteration range
using indices of rows and columns.

If no indices are specified the range starts at A1.

If no cells are in the worksheet an empty tuple will be returned.

**Parameters:**

- **min_col** (`int`) – smallest column index (1-based index)
- **min_row** (`int`) – smallest row index (1-based index)
- **max_col** (`int`) – largest column index (1-based index)
- **max_row** (`int`) – largest row index (1-based index)
- **values_only** (`bool`) – whether only cell values should be returned

### `iter_rows`

```python
iter_rows(min_row = None, max_row = None, min_col = None, max_col = None, values_only = False)
```

Produces cells from the worksheet, by row. Specify the iteration range
using indices of rows and columns.

If no indices are specified the range starts at A1.

If no cells are in the worksheet an empty tuple will be returned.

**Parameters:**

- **min_col** (`int`) – smallest column index (1-based index)
- **min_row** (`int`) – smallest row index (1-based index)
- **max_col** (`int`) – largest column index (1-based index)
- **max_row** (`int`) – largest row index (1-based index)
- **values_only** (`bool`) – whether only cell values should be returned

### `max_column`

```python
max_column
```

The maximum column index containing data (1-based)

:type: int

### `max_row`

```python
max_row
```

The maximum row index containing data (1-based)

:type: int

### `merge_cells`

```python
merge_cells(range_string = None, start_row = None, start_column = None, end_row = None, end_column = None)
```

Set merge on a cell range. Range is a cell range (e.g. A1:E1)

### `merged_cell_ranges`

```python
merged_cell_ranges
```

Return a copy of cell ranges

### `mime_type`

```python
mime_type = 'application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml'
```

### `min_column`

```python
min_column
```

The minimum column index containing data (1-based)

:type: int

### `min_row`

```python
min_row
```

The minimum row index containing data (1-based)

:type: int

### `move_range`

```python
move_range(cell_range, rows = 0, cols = 0, translate = False)
```

Move a cell range by the number of rows and/or columns:
down if rows > 0 and up if rows < 0
right if cols > 0 and left if cols < 0
Existing cells will be overwritten.
Formulae and references will not be updated.

Under ``preserve=True`` the move lands as tracked cell edits and
refuses with ``UnsupportedStructureError`` (changing nothing) when
it cannot keep the sheet coherent — e.g. merged ranges, tables,
conditional formatting or data validation intersecting either
rectangle, or outside formulas that reference the moved block.

### `print_area`

```python
print_area
```

The print area for the worksheet, or None if not set. To set, supply a range
like 'A1:D4' or a list of ranges.

### `print_title_cols`

```python
print_title_cols
```

Columns to be printed at the left side of every page (ex: 'A:C')

### `print_title_rows`

```python
print_title_rows
```

Rows to be printed at the top of every page (ex: '1:3')

### `print_titles`

```python
print_titles
```

### `replace_image`

```python
replace_image(target, replacement, *, name = None)
```

Replace one loaded image while preserving its drawing anchor.

**Parameters:**

- **target** (`Image | str`) – Loaded image or its anchor coordinate.
- **replacement** (`Image | path - like`) – Replacement image or image source.
- **name** (`str | None`) – Optional image name used to resolve an ambiguous anchor.

**Returns:**

- `openpyxl.drawing.image.Image` – The loaded image selected for replacement.

### `rows`

```python
rows
```

Produces all cells in the worksheet, by row (see `iter_rows`)

:type: generator

### `selected_cell`

```python
selected_cell
```

### `set_printer_settings`

```python
set_printer_settings(paper_size, orientation)
```

Set printer settings

### `sheet_view`

```python
sheet_view
```

### `show_gridlines`

```python
show_gridlines
```

### `tables`

```python
tables
```

### `unmerge_cells`

```python
unmerge_cells(range_string = None, start_row = None, start_column = None, end_row = None, end_column = None)
```

Remove merge on a cell range. Range is a cell range (e.g. A1:E1)

### `values`

```python
values
```

Produces all cell values in the worksheet, by row

:type: generator
