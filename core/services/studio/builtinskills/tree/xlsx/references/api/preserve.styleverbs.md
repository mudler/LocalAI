<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.styleverbs`

Small preserve-aware helpers for explicit formatting operations.

## `copy_format`

```python
copy_format(ws, src_cell, dst_range)
```

Atomically copy one cell's complete cell style onto a finite range.

The copied style includes font, fill, border, alignment, number format,
and protection. Values, formulas, comments, hyperlinks, validation, row
heights, and column widths are not copied.
