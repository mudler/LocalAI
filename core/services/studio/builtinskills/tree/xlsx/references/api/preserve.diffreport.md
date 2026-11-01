<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.diffreport`

Compare two workbook packages cell-wise, classifying differences as

## `DiffReport`

```python
DiffReport(changed, shifted, added_sheets, removed_sheets)
```

### `SCHEMA`

```python
SCHEMA = 'workbook_diff'
```

### `VERSION`

```python
VERSION = 1
```

### `added_sheets`

```python
added_sheets = added_sheets
```

### `changed`

```python
changed = changed
```

### `removed_sheets`

```python
removed_sheets = removed_sheets
```

### `shifted`

```python
shifted = shifted
```

### `to_dict`

```python
to_dict()
```

## `diff_workbooks`

```python
diff_workbooks(a, b, remaps = ())
```

A cell-level report of how package ``b`` differs from ``a``
(paths, bytes, or file-likes). ``remaps``: AddressRemap chain from
the structural edits performed between the two states — differences
explained by a remap classify as "shifted", the rest as "changed".
