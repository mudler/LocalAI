<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.dml.color`

DrawingML objects related to color, ColorFormat being the most prominent.

## `ColorFormat`

```python
ColorFormat(eg_colorChoice_parent, color)
```

Bases: `object`

Provides access to color settings such as RGB color, theme color, and
luminance adjustments.

### `brightness`

```python
brightness
```

Read/write float value between -1.0 and 1.0 indicating the brightness
adjustment for this color, e.g. -0.25 is 25% darker and 0.4 is 40%
lighter. 0 means no brightness adjustment.

### `from_colorchoice_parent`

```python
from_colorchoice_parent(eg_colorChoice_parent)
```

### `rgb`

```python
rgb
```

`RGBColor` value of this color, or None if no RGB color is explicitly
defined for this font. Setting this value to an `RGBColor` instance
causes its type to change to MSO_COLOR_TYPE.RGB. If the color was a
theme color with a brightness adjustment, the brightness adjustment
is removed when changing it to an RGB color.

### `theme_color`

```python
theme_color
```

Theme color value of this color.

Value is a member of `MsoThemeColorIndex`, e.g.
``MSO_THEME_COLOR.ACCENT_1``. Raises AttributeError on access if the
color is not type ``MSO_COLOR_TYPE.SCHEME``. Assigning a member of
`MsoThemeColorIndex` causes the color's type to change to
``MSO_COLOR_TYPE.SCHEME``.

### `type`

```python
type
```

Read-only. A value from `MsoColorType`, either RGB or SCHEME,
corresponding to the way this color is defined, or None if no color
is defined at the level of this font.

## `RGBColor`

Bases: `tuple`

Immutable value object defining a particular RGB color.

### `from_string`

```python
from_string(rgb_hex_str)
```

Return a new instance from an RGB color hex string like ``'3C2F80'``.
