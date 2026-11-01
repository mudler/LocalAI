<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.dml.fill`

DrawingML objects related to fill.

## `FillFormat`

```python
FillFormat(eg_fill_properties_parent: BaseOxmlElement, fill_obj: _Fill)
```

Bases: `object`

Provides access to the current fill properties.

Also provides methods to change the fill type.

### `back_color`

```python
back_color
```

Return a `ColorFormat` object representing background color.

This property is only applicable to pattern fills and lines.

### `background`

```python
background()
```

Sets the fill type to noFill, i.e. transparent.

### `fore_color`

```python
fore_color
```

Return a `ColorFormat` instance representing the foreground color of
this fill.

### `from_fill_parent`

```python
from_fill_parent(eg_fillProperties_parent: BaseOxmlElement) -> FillFormat
```

Return a `FillFormat` instance initialized to the settings contained
in *eg_fillProperties_parent*, which must be an element having
EG_FillProperties in its child element sequence in the XML schema.

### `gradient`

```python
gradient()
```

Sets the fill type to gradient.

If the fill is not already a gradient, a default gradient is added.
The default gradient corresponds to the default in the built-in
PowerPoint "White" template. This gradient is linear at angle
90-degrees (upward), with two stops. The first stop is Accent-1 with
tint 100%, shade 100%, and satMod 130%. The second stop is Accent-1
with tint 50%, shade 100%, and satMod 350%.

### `gradient_angle`

```python
gradient_angle
```

Angle in float degrees of line of a linear gradient.

Read/Write. May be `None`, indicating the angle should be inherited
from the style hierarchy. An angle of 0.0 corresponds to
a left-to-right gradient. Increasing angles represent
counter-clockwise rotation of the line, for example 90.0 represents
a bottom-to-top gradient. Raises `TypeError` when the fill type is
not MSO_FILL_TYPE.GRADIENT. Raises `ValueError` for a non-linear
gradient (e.g. a radial gradient).

### `gradient_stops`

```python
gradient_stops
```

`GradientStops` object providing access to stops of this gradient.

Raises `TypeError` when fill is not gradient (call `fill.gradient()`
first). Each stop represents a color between which the gradient
smoothly transitions.

### `pattern`

```python
pattern
```

Return member of `MsoPatternType` indicating fill pattern.

Raises `TypeError` when fill is not patterned (call
`fill.patterned()` first). Returns `None` if no pattern has been set;
PowerPoint may display the default `PERCENT_5` pattern in this case.
Assigning `None` will remove any explicit pattern setting, although
relying on the default behavior is discouraged and may produce
rendering differences across client applications.

### `patterned`

```python
patterned()
```

Selects the pattern fill type.

Note that calling this method does not by itself set a foreground or
background color of the pattern. Rather it enables subsequent
assignments to properties like fore_color to set the pattern and
colors.

### `solid`

```python
solid()
```

Sets the fill type to solid, i.e. a solid color. Note that calling
this method does not set a color or by itself cause the shape to
appear with a solid color fill; rather it enables subsequent
assignments to properties like fore_color to set the color.

### `type`

```python
type: MSO_FILL_TYPE
```

The type of this fill, e.g. `MSO_FILL_TYPE.SOLID`.
