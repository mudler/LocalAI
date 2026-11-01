<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes.autoshape`

Autoshape-related objects such as Shape and Adjustment.

## `Adjustment`

```python
Adjustment(name: str, def_val: int, actual: int | None = None)
```

An adjustment value for an autoshape.

An adjustment value corresponds to the position of an adjustment handle on an auto shape.
Adjustment handles are the small yellow diamond-shaped handles that appear on certain auto
shapes and allow the outline of the shape to be adjusted. For example, a rounded rectangle has
an adjustment handle that allows the radius of its corner rounding to be adjusted.

Values are `float` and generally range from 0.0 to 1.0, although the value can be negative or
greater than 1.0 in certain circumstances.

### `actual`

```python
actual = actual
```

### `def_val`

```python
def_val = def_val
```

### `effective_value`

```python
effective_value: float
```

Read/write `float` representing normalized adjustment value for this adjustment.

Actual values are a large-ish integer expressed in shape coordinates, nominally between 0
and 100,000. The effective value is normalized to a corresponding value nominally between
0.0 and 1.0. Intuitively this represents the proportion of the width or height of the shape
at which the adjustment value is located from its starting point. For simple shapes such as
a rounded rectangle, this intuitive correspondence holds. For more complicated shapes and
at more extreme shape proportions (e.g. width is much greater than height), the value can
become negative or greater than 1.0.

### `name`

```python
name = name
```

### `val`

```python
val: int
```

Denormalized effective value.

Expressed in shape coordinates, this is suitable for using in the XML.

## `AdjustmentCollection`

```python
AdjustmentCollection(prstGeom: CT_PresetGeometry2D)
```

Sequence of `Adjustment` instances for an auto shape.

Each represents an available adjustment for a shape of its type. Supports `len()` and indexed
access, e.g. `shape.adjustments[1] = 0.15`.

## `AutoShapeType`

```python
AutoShapeType(autoshape_type_id: MSO_AUTO_SHAPE_TYPE)
```

Provides access to metadata for an auto-shape of type identified by `autoshape_type_id`.

Instances are cached, so no more than one instance for a particular auto shape type is in
memory.

Instances provide the following attributes:

.. attribute:: autoshape_type_id

   Integer uniquely identifying this auto shape type. Corresponds to a
   value in `pptx.constants.MSO` like `MSO_SHAPE.ROUNDED_RECTANGLE`.

.. attribute:: basename

   Base part of shape name for auto shapes of this type, e.g. `Rounded
   Rectangle` becomes `Rounded Rectangle 99` when the distinguishing
   integer is added to the shape name.

.. attribute:: prst

   String identifier for this auto shape type used in the `a:prstGeom`
   element.

Initialize attributes from constant values in `pptx.spec`.

### `autoshape_type_id`

```python
autoshape_type_id: MSO_AUTO_SHAPE_TYPE
```

MSO_AUTO_SHAPE_TYPE enumeration member identifying this auto shape type.

### `basename`

```python
basename: str
```

Base of shape name for this auto shape type.

A shape name is like "Rounded Rectangle 7" and appears as an XML attribute for example at
`p:sp/p:nvSpPr/p:cNvPr{name}`. This basename value is the name less the distinguishing
integer. This value is escaped because at least one autoshape-type name includes double
quotes ('"No" Symbol').

### `default_adjustment_values`

```python
default_adjustment_values(prst: MSO_AUTO_SHAPE_TYPE) -> tuple[AdjustmentValue, ...]
```

Sequence of (name, value) pair adjustment value defaults for `prst` autoshape-type.

### `id_from_prst`

```python
id_from_prst(prst: str) -> MSO_AUTO_SHAPE_TYPE
```

Select auto shape type with matching `prst`.

e.g. `MSO_SHAPE.RECTANGLE` corresponding to preset geometry keyword `"rect"`.

### `prst`

```python
prst
```

Preset geometry identifier string for this auto shape. Used in the
`prst` attribute of `a:prstGeom` element to specify the geometry
to be used in rendering the shape, for example `'roundRect'`.

## `Shape`

```python
Shape(sp: CT_Shape, parent: ProvidesPart)
```

Bases: `BaseShape`

A shape that can appear on a slide.

Corresponds to the `p:sp` element that can appear in any of the slide-type parts
(slide, slideLayout, slideMaster, notesPage, notesMaster, handoutMaster).

### `adjustments`

```python
adjustments() -> AdjustmentCollection
```

Read-only reference to `AdjustmentCollection` instance for this shape.

### `auto_shape_type`

```python
auto_shape_type
```

Enumeration value identifying the type of this auto shape.

Like `MSO_SHAPE.ROUNDED_RECTANGLE`. Raises `ValueError` if this shape is not an auto shape.

### `fill`

```python
fill()
```

`FillFormat` instance for this shape.

Provides access to fill properties such as fill color.

### `get_or_add_ln`

```python
get_or_add_ln()
```

Return the `a:ln` element containing the line format properties XML for this shape.

### `has_text_frame`

```python
has_text_frame: bool
```

`True` if this shape can contain text. Always `True` for an AutoShape.

### `line`

```python
line()
```

`LineFormat` instance for this shape.

Provides access to line properties such as line color.

### `ln`

```python
ln
```

The `a:ln` element containing the line format properties such as line color and width.

`None` if no `a:ln` element is present.

### `shape_type`

```python
shape_type: MSO_SHAPE_TYPE
```

Unique integer identifying the type of this shape, like `MSO_SHAPE_TYPE.TEXT_BOX`.

### `text`

```python
text: str
```

Read/write. Text in shape as a single string.

The returned string will contain a newline character (`"\n"`) separating each paragraph
and a vertical-tab (`"\v"`) character for each line break (soft carriage return) in the
shape's text.

Assignment to `text` replaces any text previously contained in the shape, along with any
paragraph or font formatting applied to it. A newline character (`"\n"`) in the assigned
text causes a new paragraph to be started. A vertical-tab (`"\v"`) character in the
assigned text causes a line-break (soft carriage-return) to be inserted. (The vertical-tab
character appears in clipboard text copied from PowerPoint as its str encoding of
line-breaks.)

### `text_frame`

```python
text_frame
```

`TextFrame` instance for this shape.

Contains the text of the shape and provides access to text formatting properties.
