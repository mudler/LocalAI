<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.validation`

Read deterministic list data-validation values for a worksheet cell.

## `allowed_values`

```python
allowed_values(ws, cell)
```

Return deterministic list-validation values covering ``cell``.

``None`` means no list validation covers the cell. A matching validation
whose vocabulary cannot be reported exactly raises a typed refusal.
Blank cells in a static source range are returned as ``None`` entries.

**Parameters:**

- **ws** (`Worksheet`) – Worksheet containing the validated cell.
- **cell** (`Cell | str`) – Cell object or coordinate to inspect.

**Returns:**

- `list | None` – Allowed values, or ``None`` when no list validation applies.
