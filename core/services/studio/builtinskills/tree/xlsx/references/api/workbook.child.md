<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.workbook.child`

## `INVALID_TITLE_REGEX`

```python
INVALID_TITLE_REGEX = re.compile('[\\\\*?:/\\[\\]]')
```

## `avoid_duplicate_name`

```python
avoid_duplicate_name(names, value)
```

Naive check to see whether name already exists.
If name does exist suggest a name using an incrementer
Duplicates are case insensitive
