<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.dml.color`

DrawingML objects related to color, ColorFormat being the most prominent.

## `ColorFormat`

```python
ColorFormat(rPr_parent: RPrParent)
```

Bases: `ElementProxy`

Provides access to color settings like RGB color, theme color, and luminance adjustments.

### `rgb`

```python
rgb: RGBColor | None
```

An `RGBColor` value or `None` if no RGB color is specified.

When `type` is `MSO_COLOR_TYPE.RGB`, the value of this property will always be an
`RGBColor` value. It may also be an `RGBColor` value if `type` is
`MSO_COLOR_TYPE.THEME`, as Word writes the current value of a theme color when one is
assigned. In that case, the RGB value should be interpreted as no more than a good guess
however, as the theme color takes precedence at rendering time. Its value is `None`
whenever `type` is either `None` or `MSO_COLOR_TYPE.AUTO`.

Assigning an `RGBColor` value causes `type` to become `MSO_COLOR_TYPE.RGB` and any
theme color is removed. Assigning `None` causes any color to be removed such that the
effective color is inherited from the style hierarchy.

### `theme_color`

```python
theme_color: MSO_THEME_COLOR | None
```

Member of `MsoThemeColorIndex` or `None` if no theme color is specified.

When `type` is `MSO_COLOR_TYPE.THEME`, the value of this property will always be a
member of `MsoThemeColorIndex`. When `type` has any other value, the value of
this property is `None`.

Assigning a member of `MsoThemeColorIndex` causes `type` to become
`MSO_COLOR_TYPE.THEME`. Any existing RGB value is retained but ignored by Word. Assigning
`None` causes any color specification to be removed such that the effective color is
inherited from the style hierarchy.

### `type`

```python
type: MSO_COLOR_TYPE | None
```

Read-only.

A member of `MsoColorType`, one of RGB, THEME, or AUTO, corresponding to the way this
color is defined. Its value is `None` if no color is applied at this level, which causes
the effective color to be inherited from the style hierarchy.

## `RPrParent`

```python
RPrParent: TypeAlias = 'CT_R'
```
