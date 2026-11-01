<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.preserve.sourceio`

Read package sources without imposing workbook eligibility caps.

## `read_source_bytes`

```python
read_source_bytes(source, *, context = 'workbook package')
```

Read package bytes while preserving a seekable stream's position.

**Parameters:**

- **source** (`bytes | path-like | binary file-like`) – Package bytes, path, or seekable binary stream.
- **context** (`str`) – Name used in source validation errors.

**Returns:**

- `bytes` – Complete package contents.
