<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.api`

Directly exposed API classes, Presentation for now.

## `Presentation`

```python
Presentation(pptx: str | IO[bytes] | None = None) -> presentation.Presentation
```

Return a `Presentation` object loaded from *pptx*, where *pptx* can be
either a path to a ``.pptx`` file (a string) or a file-like object. If
*pptx* is missing or ``None``, the built-in default presentation
"template" is loaded.
