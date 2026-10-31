<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.text.fonts`

Objects related to system font file lookup.

## `FontFiles`

Bases: `object`

A class-based singleton serving as a lazy cache for system font details.

### `find`

```python
find(family_name: str, is_bold: bool, is_italic: bool) -> str
```

Return the absolute path to an installed OpenType font.

File is matched by `family_name` and the styles `is_bold` and `is_italic`.
