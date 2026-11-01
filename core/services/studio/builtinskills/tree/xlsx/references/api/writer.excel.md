<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.writer.excel`

## `ExcelWriter`

```python
ExcelWriter(workbook, archive)
```

Write a workbook object to an Excel file.

### `manifest`

```python
manifest = Manifest()
```

### `save`

```python
save()
```

Write data into the archive.

### `vba_modified`

```python
vba_modified = set()
```

### `workbook`

```python
workbook = workbook
```

### `write_data`

```python
write_data()
```

### `write_worksheet`

```python
write_worksheet(ws)
```

## `WorkbookWriter`

```python
WorkbookWriter(wb)
```

### `package`

```python
package = WorkbookPackage()
```

### `rels`

```python
rels = RelationshipList()
```

### `wb`

```python
wb = wb
```

### `write`

```python
write()
```

Write the core workbook xml.

### `write_names`

```python
write_names()
```

### `write_pivots`

```python
write_pivots()
```

### `write_properties`

```python
write_properties()
```

### `write_refs`

```python
write_refs()
```

### `write_rels`

```python
write_rels()
```

Write the workbook relationships xml.

### `write_root_rels`

```python
write_root_rels()
```

Write the package relationships

### `write_views`

```python
write_views()
```

### `write_worksheets`

```python
write_worksheets()
```

## `WorksheetWriter`

```python
WorksheetWriter(ws, out = None)
```

### `cleanup`

```python
cleanup()
```

Remove tempfile

### `close`

```python
close()
```

Close the context manager

### `get_stream`

```python
get_stream()
```

### `out`

```python
out = out
```

### `read`

```python
read()
```

Close the context manager and return serialised XML

### `rows`

```python
rows()
```

Return all rows, and any cells that they contain

### `write`

```python
write()
```

High level

### `write_breaks`

```python
write_breaks()
```

### `write_cols`

```python
write_cols()
```

### `write_dimensions`

```python
write_dimensions()
```

Write worksheet size if known

### `write_drawings`

```python
write_drawings()
```

### `write_filter`

```python
write_filter()
```

### `write_format`

```python
write_format()
```

### `write_formatting`

```python
write_formatting()
```

### `write_header`

```python
write_header()
```

### `write_hyperlinks`

```python
write_hyperlinks()
```

### `write_legacy`

```python
write_legacy()
```

Comments & VBA controls use VML and require an additional element
that is no longer in the specification.

### `write_margins`

```python
write_margins()
```

### `write_merged_cells`

```python
write_merged_cells()
```

### `write_page`

```python
write_page()
```

### `write_print`

```python
write_print()
```

### `write_properties`

```python
write_properties()
```

### `write_protection`

```python
write_protection()
```

### `write_row`

```python
write_row(xf, row, row_idx)
```

### `write_rows`

```python
write_rows()
```

### `write_scenarios`

```python
write_scenarios()
```

### `write_sort`

```python
write_sort()
```

As per discusion with the OOXML Working Group global sort state is not required.
openpyxl never reads it from existing files

### `write_tables`

```python
write_tables()
```

### `write_tail`

```python
write_tail()
```

Write all elements after the rows
calc properties
protection
protected ranges #
scenarios
filters
sorts # always ignored
data consolidation #
custom views #
merged cells
phonetic properties #
conditional formatting
data validation
hyperlinks
print options
page margins
page setup
header
row breaks
col breaks
custom properties #
cell watches #
ignored errors #
smart tags #
drawing
drawingHF #
background #
OLE objects #
controls #
web publishing #
tables

### `write_top`

```python
write_top()
```

Write all elements up to rows:
properties
dimensions
views
format
cols

### `write_validations`

```python
write_validations()
```

### `write_views`

```python
write_views()
```

### `ws`

```python
ws = ws
```

### `xf`

```python
xf = self.get_stream()
```

## `save_workbook`

```python
save_workbook(workbook, filename, *, allow_formula_loss = False)
```

Save the given workbook on the filesystem under the name filename.

**Parameters:**

- **workbook** (``openpyxl.workbook.Workbook``) – the workbook to save
- **filename** (`string`) – the path to which save the workbook
