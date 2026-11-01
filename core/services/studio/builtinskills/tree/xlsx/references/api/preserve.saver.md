<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.saver`

Save dispatch target for preserve-mode workbooks.

## `save_preserved`

```python
save_preserved(workbook, target, *, allow_formula_loss = False)
```

Plan and deliver without retaining serializer side effects.

## `validate_preserved`

```python
validate_preserved(workbook, *, allow_formula_loss = False)
```

Run the exact preserve save planner without assembling an archive.

Serializer helpers used during planning can mutate model registries, so
validation uses the same planning-state guard as delivery and restores the
workbook before returning or raising.

**Parameters:**

- **workbook** (`Workbook`) – Preserve-mode workbook to validate.
- **allow_formula_loss** (`bool`) – Allow edited cached values to replace formulas.

**Returns:**

- `None` – ``None``.
