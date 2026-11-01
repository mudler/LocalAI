<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.api`

Directly exposed API functions and classes, `Document` for now.

## `Document`

```python
Document(docx: str | IO[bytes] | None = None) -> DocumentObject
```

Return a `Document` object loaded from `docx`, where `docx` can be either a path
to a ``.docx`` file (a string) or a file-like object.

If `docx` is missing or ``None``, the built-in default document "template" is
loaded.
