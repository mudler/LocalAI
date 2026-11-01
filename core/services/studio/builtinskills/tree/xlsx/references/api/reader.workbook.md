<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.reader.workbook`

## `WorkbookParser`

```python
WorkbookParser(archive, workbook_part_name, keep_links = True)
```

### `archive`

```python
archive = archive
```

### `assign_names`

```python
assign_names()
```

Bind defined names and other definitions to worksheets or the workbook

### `defined_names`

```python
defined_names = DefinedNameList()
```

### `find_sheets`

```python
find_sheets()
```

Find all sheets in the workbook and return the link to the source file.

Older XLSM files sometimes contain invalid sheet elements.
Warn user when these are removed.

### `keep_links`

```python
keep_links = keep_links
```

### `parse`

```python
parse()
```

### `pivot_caches`

```python
pivot_caches
```

Get PivotCache objects

### `rels`

```python
rels
```

### `sheets`

```python
sheets = []
```

### `wb`

```python
wb = Workbook()
```

### `workbook_part_name`

```python
workbook_part_name = workbook_part_name
```
