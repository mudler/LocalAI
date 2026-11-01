<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.dml.line`

DrawingML objects related to line formatting.

## `LineFormat`

```python
LineFormat(parent)
```

Bases: `object`

Provides access to line properties such as color, style, and width.

A LineFormat object is typically accessed via the ``.line`` property of
a shape such as `Shape` or `Picture`.

### `color`

```python
color()
```

The `ColorFormat` instance that provides access to the color settings
for this line. Essentially a shortcut for ``line.fill.fore_color``.
As a side-effect, accessing this property causes the line fill type
to be set to ``MSO_FILL.SOLID``. If this sounds risky for your use
case, use ``line.fill.type`` to non-destructively discover the
existing fill type.

### `dash_style`

```python
dash_style
```

Return value indicating line style.

Returns a member of `MsoLineDashStyle` indicating line style, or
`None` if no explicit value has been set. When no explicit value has
been set, the line dash style is inherited from the style hierarchy.

Assigning `None` removes any existing explicitly-defined dash style.

### `fill`

```python
fill()
```

`FillFormat` instance for this line, providing access to fill
properties such as foreground color.

### `width`

```python
width
```

The width of the line expressed as an integer number of `English
Metric Units`. The returned value is an instance of `Length`,
a value class having properties such as `.inches`, `.cm`, and `.pt`
for converting the value into convenient units.
