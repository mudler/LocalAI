<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.images`

Replace one drawing relationship without rewriting the drawing.

## `plan_replacements`

```python
plan_replacements(zin, requests, part_plan, names, plan)
```

Add replacement media parts and retarget their relationships.

**Parameters:**

- **zin** (`ZipFile`) – Open workbook package.
- **requests** (`mapping`) – Validated replacement requests keyed by image identity.
- **part_plan** (`PartPlan`) – Package lifecycle plan to update.
- **names** (`set[str]`) – Part names present in the package.
- **plan** (`dict`) – Planned replacement bytes keyed by part name.

**Returns:**

- `None` – ``None``.

## `request_replacement`

```python
request_replacement(ws, target, replacement, *, name = None)
```

Validate and record one relationship-owned image replacement.

**Parameters:**

- **ws** (`Worksheet`) – Worksheet containing the loaded image.
- **target** (`Image | str`) – Loaded image or its anchor coordinate.
- **replacement** (`Image | path - like`) – Replacement image or image source.
- **name** (`str | None`) – Optional image name used to resolve an ambiguous anchor.

**Returns:**

- `openpyxl.drawing.image.Image` – The loaded image selected for replacement.
