<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.text.layout`

Objects related to layout of rendered text, such as TextFitter.

## `TextFitter`

Bases: `tuple`

Value object that knows how to fit text into given rectangular extents.

### `best_fit_font_size`

```python
best_fit_font_size(text: str, extents: tuple[Length, Length], max_size: int, font_file: str) -> int
```

Return whole-number best fit point size less than or equal to `max_size`.

The return value is the largest whole-number point size less than or equal to
`max_size` that allows `text` to fit completely within `extents` when rendered
using font defined in `font_file`.
