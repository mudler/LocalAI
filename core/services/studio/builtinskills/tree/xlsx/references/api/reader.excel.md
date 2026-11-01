<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.reader.excel`

Read an xlsx file into Python

## `ExcelReader`

```python
ExcelReader(fn, read_only = False, keep_vba = KEEP_VBA, data_only = False, keep_links = True, rich_text = False, *, preserve = False)
```

Read an Excel package and dispatch the contents to the relevant modules

### `archive`

```python
archive = _validate_archive(fn, preserve=preserve)
```

### `data_only`

```python
data_only = data_only
```

### `keep_links`

```python
keep_links = keep_links
```

### `keep_vba`

```python
keep_vba = keep_vba
```

### `preserve`

```python
preserve = preserve
```

### `read`

```python
read()
```

### `read_chartsheet`

```python
read_chartsheet(sheet, rel)
```

### `read_custom`

```python
read_custom()
```

### `read_manifest`

```python
read_manifest()
```

### `read_only`

```python
read_only = read_only
```

### `read_properties`

```python
read_properties()
```

### `read_strings`

```python
read_strings()
```

### `read_theme`

```python
read_theme()
```

### `read_workbook`

```python
read_workbook()
```

### `read_worksheets`

```python
read_worksheets()
```

### `rich_text`

```python
rich_text = rich_text
```

### `shared_strings`

```python
shared_strings = []
```

### `valid_files`

```python
valid_files = self.archive.namelist()
```

## `KEEP_VBA`

```python
KEEP_VBA = os.environ.get('OPENPYXL_KEEP_VBA', 'False') == 'True'
```

## `ReadOnlyWorksheet`

```python
ReadOnlyWorksheet(parent_workbook, title, worksheet_path, shared_strings)
```

### `calculate_dimension`

```python
calculate_dimension(force = False)
```

### `cell`

```python
cell = Worksheet.cell
```

### `defined_names`

```python
defined_names = DefinedNameDict()
```

### `iter_rows`

```python
iter_rows = Worksheet.iter_rows
```

### `max_column`

```python
max_column
```

### `max_row`

```python
max_row
```

### `min_column`

```python
min_column
```

### `min_row`

```python
min_row
```

### `parent`

```python
parent = parent_workbook
```

### `reset_dimensions`

```python
reset_dimensions()
```

Remove worksheet dimensions if these are incorrect in the worksheet source.
NB. This probably indicates a bug in the library or application that created
the workbook.

### `rows`

```python
rows = Worksheet.rows
```

### `sheet_state`

```python
sheet_state = 'visible'
```

### `title`

```python
title = title
```

### `values`

```python
values = Worksheet.values
```

## `SUPPORTED_FORMATS`

```python
SUPPORTED_FORMATS = ('.xlsx', '.xlsm', '.xltx', '.xltm')
```

## `WorksheetReader`

```python
WorksheetReader(ws, xml_source, shared_strings, data_only, rich_text, *, warn_extensions = True)
```

Create a parser and apply it to a workbook

### `bind_all`

```python
bind_all()
```

### `bind_cells`

```python
bind_cells()
```

### `bind_col_dimensions`

```python
bind_col_dimensions()
```

### `bind_formatting`

```python
bind_formatting()
```

### `bind_hyperlinks`

```python
bind_hyperlinks()
```

### `bind_merged_cells`

```python
bind_merged_cells()
```

### `bind_properties`

```python
bind_properties()
```

### `bind_row_dimensions`

```python
bind_row_dimensions()
```

### `bind_tables`

```python
bind_tables()
```

### `normalize_merged_cell_link`

```python
normalize_merged_cell_link(coord)
```

Returns the appropriate cell to which a hyperlink, which references a merged cell at the specified coordinates,
should be bound.

### `parser`

```python
parser = WorkSheetParser(xml_source, shared_strings, data_only, ws.parent.epoch, ws.parent._date_formats, ws.parent._timedelta_formats, rich_text, warn_extensions=warn_extensions)
```

### `tables`

```python
tables = []
```

### `ws`

```python
ws = ws
```

## `load_workbook`

```python
load_workbook(filename, read_only = False, keep_vba = KEEP_VBA, data_only = False, keep_links = True, rich_text = False, *, preserve = None)
```

Open the given filename and return the workbook

**Parameters:**

- **filename** (`string | a file-like object open in binary mode c.f., `zipfile.ZipFile``) – the path to open or a file-like object
- **read_only** (`bool`) – optimised for reading, content cannot be edited
- **keep_vba** (`bool`) – preserve vba content (this does NOT mean you can use it)
- **data_only** (`bool`) – controls whether cells with formulae have either the formula (default) or the value stored the last time Excel read the sheet
- **keep_links** (`bool`) – whether links to external workbooks should be preserved. The default is True
- **rich_text** (`bool`) – if set to True openpyxl will preserve any rich text formatting in cells. The default is False
- **preserve** (`bool | None`) – control preserve mode: the original package bytes are
retained as the source of truth and save becomes a lossless splice of
recorded edits into them. Content openpyxl does not model (charts,
drawings, VBA, pivot caches, extensions) survives byte-identical.
Unsafe operations raise a typed `openpyxl.errors.PaperRefusal`
instead of proceeding lossily. The default ``None`` enables preserve
mode for editable OOXML workbooks. Read-only and unsupported-format
loads retain stock behavior. Pass ``False`` explicitly to opt into the
stock, potentially lossy round trip.
