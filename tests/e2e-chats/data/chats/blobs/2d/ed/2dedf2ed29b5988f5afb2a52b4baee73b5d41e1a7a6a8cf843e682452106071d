<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.workbook.workbook`

Workbook is the top-level container for all document information.

## `INTEGER_TYPES`

```python
INTEGER_TYPES = (int,)
```

## `Workbook`

```python
Workbook(write_only = False, iso_dates = False)
```

Workbook is the container for all other parts of the document.

### `active`

```python
active
```

Get the currently active sheet or None

:type: `openpyxl.worksheet.worksheet.Worksheet`

### `add_named_style`

```python
add_named_style(style)
```

Add a named style

### `calculation`

```python
calculation = CalcProperties()
```

### `chartsheets`

```python
chartsheets
```

A list of Chartsheets in this workbook

:type: list of `openpyxl.chartsheet.chartsheet.Chartsheet`

### `close`

```python
close()
```

Close workbook file if open. Only affects read-only and write-only modes.

### `code_name`

```python
code_name = None
```

### `copy_worksheet`

```python
copy_worksheet(from_worksheet)
```

Copy an existing worksheet in the current workbook

.. warning::
    This function cannot copy worksheets between workbooks.
    worksheets can only be copied within the workbook that they belong

**Parameters:**

- **from_worksheet** – the worksheet to be copied from

**Returns:**

-  – copy of the initial worksheet

### `create_chartsheet`

```python
create_chartsheet(title = None, index = None)
```

### `create_named_range`

```python
create_named_range(name, worksheet = None, value = None, scope = None)
```

Create a new named_range on a worksheet

### `create_sheet`

```python
create_sheet(title = None, index = None)
```

Create a worksheet (at an optional index).

**Parameters:**

- **title** (`str`) – optional title of the sheet
- **index** (`int`) – optional position at which the sheet will be inserted

### `custom_doc_props`

```python
custom_doc_props = CustomPropertyList()
```

### `data_only`

```python
data_only
```

### `defined_names`

```python
defined_names = DefinedNameDict()
```

### `encoding`

```python
encoding = 'utf-8'
```

### `epoch`

```python
epoch
```

### `excel_base_date`

```python
excel_base_date
```

### `get_index`

```python
get_index(worksheet)
```

Return the index of the worksheet.

### `get_sheet_by_name`

```python
get_sheet_by_name(name)
```

Returns a worksheet by its name.

**Parameters:**

- **name** (`string`) – the name of the worksheet to look for

### `get_sheet_names`

```python
get_sheet_names()
```

### `index`

```python
index(worksheet)
```

Return the index of a worksheet.

### `is_template`

```python
is_template = False
```

### `iso_dates`

```python
iso_dates = iso_dates
```

### `loaded_theme`

```python
loaded_theme = None
```

### `mime_type`

```python
mime_type
```

The mime type is determined by whether a workbook is a template or
not and whether it contains macros or not. Excel requires the file
extension to match but openpyxl does not enforce this.

### `move_sheet`

```python
move_sheet(sheet, offset = 0)
```

Move a sheet or sheetname

### `named_styles`

```python
named_styles
```

List available named styles

### `path`

```python
path = '/xl/workbook.xml'
```

### `preserve`

```python
preserve
```

True when this workbook was loaded with ``preserve=True``: the
original package bytes are the source of truth and save is a
lossless splice of recorded edits into them.

### `properties`

```python
properties = DocumentProperties()
```

### `read_only`

```python
read_only
```

### `rels`

```python
rels = RelationshipList()
```

### `remove`

```python
remove(worksheet)
```

Remove `worksheet` from this workbook.

Under preserve mode a loaded sheet's removal runs the reference
audit first. The package-part cascade happens at save.

### `remove_sheet`

```python
remove_sheet(worksheet)
```

Remove `worksheet` from this workbook.

### `save`

```python
save(filename, *, allow_formula_loss = False, receipt = False)
```

Save the current workbook under the given `filename`.
Use this function instead of using an `ExcelWriter`.

**Parameters:**

- **allow_formula_loss** – a workbook loaded with ``data_only=True``
holds cached values instead of formulas, so saving destroys
formulas. Under preserve mode such a save refuses unless this
flag is set (and even then only cells you actually edited lose
their formulas — untouched cells keep them in the original
bytes). On the stock path the flag silences the loud warning.
- **receipt** – preserve mode only — return an
    `openpyxl.preserve.receipts.EditReceipt` comparing the
    saved file against the AS-LOADED source bytes. NOTE: after several saves from one session the receipt
    is cumulative — it describes the session, not the last call.

.. warning::
    When creating your workbook using `write_only` set to True,
    you will only be able to call this function once. Subsequent attempts to
    modify or save the file will raise an `openpyxl.shared.exc.WorkbookAlreadySaved` exception.

### `search`

```python
search(text_or_regex, *, regex = False, values = True, formulas = True)
```

Find text across the workbook (paper-xlsx).
Returns ``[{"address", "match", "kind"}, ...]`` where kind is
"value" or "formula".

### `security`

```python
security = DocumentSecurity()
```

### `set_pivot_refresh_on_load`

```python
set_pivot_refresh_on_load(pivots = None, *, all = False)
```

Request refresh-on-load for named pivots or explicitly all pivots.

Pivot names may be sheet-qualified (``"Sheet!PivotName"``). Multiple
pivots that share a cache necessarily share this cache-level setting.

### `shared_strings`

```python
shared_strings = IndexedList()
```

### `sheetnames`

```python
sheetnames
```

Returns the list of the names of worksheets in this workbook.

Names are returned in the worksheets order.

:type: list of strings

### `strict_protection`

```python
strict_protection = False
```

### `style_names`

```python
style_names
```

List of named styles

### `template`

```python
template = False
```

### `validate`

```python
validate()
```

Run the preserve save planner without assembling an archive.

### `vba_archive`

```python
vba_archive = None
```

### `views`

```python
views = [BookView()]
```

### `worksheets`

```python
worksheets
```

A list of sheets in this workbook

:type: list of `openpyxl.worksheet.worksheet.Worksheet`

### `write_only`

```python
write_only
```

## `WriteOnlyWorksheet`

```python
WriteOnlyWorksheet(parent, title)
```

Bases: `_WorkbookChild`

Streaming worksheet. Optimised to reduce memory by writing rows just in
time.
Cells can be styled and have comments Styles for rows and columns
must be applied before writing cells

### `add_chart`

```python
add_chart = Worksheet.add_chart
```

### `add_image`

```python
add_image = Worksheet.add_image
```

### `add_table`

```python
add_table = Worksheet.add_table
```

### `append`

```python
append(row)
```

:param row: iterable containing values to append
:type row: iterable

### `close`

```python
close()
```

### `closed`

```python
closed
```

### `freeze_panes`

```python
freeze_panes = Worksheet.freeze_panes
```

### `mime_type`

```python
mime_type = Worksheet.mime_type
```

### `print_area`

```python
print_area = Worksheet.print_area
```

### `print_title_cols`

```python
print_title_cols = Worksheet.print_title_cols
```

### `print_title_rows`

```python
print_title_rows = Worksheet.print_title_rows
```

### `print_titles`

```python
print_titles = Worksheet.print_titles
```

### `sheet_view`

```python
sheet_view = Worksheet.sheet_view
```

### `tables`

```python
tables = Worksheet.tables
```
