<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.hygiene`

Report cached error values and broken formula references.

## `ERROR_TOKENS`

```python
ERROR_TOKENS = frozenset(EXCEL_ERROR_CODES)
```

## `current_titles_by_part`

```python
current_titles_by_part(wb, zin)
```

Map worksheet part names to current live titles.

## `scan_errors`

```python
scan_errors(wb)
```

Return cached values and actual formula error operands.
