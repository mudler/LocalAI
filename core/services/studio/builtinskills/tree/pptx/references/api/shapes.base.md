<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes.base`

Base shape-related objects such as BaseShape.

## `BaseShape`

```python
BaseShape(shape_elm: ShapeElement, parent: ProvidesPart)
```

Bases: `object`

Base class for shape objects.

Subclasses include `Shape`, `Picture`, and `GraphicFrame`.

### `click_action`

```python
click_action() -> ActionSetting
```

`ActionSetting` instance providing access to click behaviors.

Click behaviors are hyperlink-like behaviors including jumping to a hyperlink (web page)
or to another slide in the presentation. The click action is that defined on the overall
shape, not a run of text within the shape. An `ActionSetting` object is always returned,
even when no click behavior is defined on the shape.

### `element`

```python
element: ShapeElement
```

`lxml` element for this shape, e.g. a CT_Shape instance.

Note that manipulating this element improperly can produce an invalid presentation file.
Make sure you know what you're doing if you use this to change the underlying XML.

### `has_chart`

```python
has_chart: bool
```

`True` if this shape is a graphic frame containing a chart object.

`False` otherwise. When `True`, the chart object can be accessed using the ``.chart``
property.

### `has_table`

```python
has_table: bool
```

`True` if this shape is a graphic frame containing a table object.

`False` otherwise. When `True`, the table object can be accessed using the ``.table``
property.

### `has_text_frame`

```python
has_text_frame: bool
```

`True` if this shape can contain text.

### `height`

```python
height: Length
```

Read/write. Integer distance between top and bottom extents of shape in EMUs.

### `is_placeholder`

```python
is_placeholder: bool
```

True if this shape is a placeholder.

A shape is a placeholder if it has a <p:ph> element.

### `left`

```python
left: Length
```

Integer distance of the left edge of this shape from the left edge of the slide.

Read/write. Expressed in English Metric Units (EMU)

### `name`

```python
name: str
```

Name of this shape, e.g. 'Picture 7'.

### `part`

```python
part: BaseSlidePart
```

The package part containing this shape.

A `BaseSlidePart` subclass in this case. Access to a slide part should only be required if
you are extending the behavior of `pp` API objects.

### `placeholder_format`

```python
placeholder_format: _PlaceholderFormat
```

Provides access to placeholder-specific properties such as placeholder type.

Raises `ValueError` on access if the shape is not a placeholder.

### `rotation`

```python
rotation: float
```

Degrees of clockwise rotation.

Read/write float. Negative values can be assigned to indicate counter-clockwise rotation,
e.g. assigning -45.0 will change setting to 315.0.

### `shadow`

```python
shadow() -> ShadowFormat
```

`ShadowFormat` object providing access to shadow for this shape.

A `ShadowFormat` object is always returned, even when no shadow is
explicitly defined on this shape (i.e. it inherits its shadow
behavior).

### `shape_id`

```python
shape_id: int
```

Read-only positive integer identifying this shape.

The id of a shape is unique among all shapes on a slide.

### `shape_type`

```python
shape_type: MSO_SHAPE_TYPE
```

A member of MSO_SHAPE_TYPE classifying this shape by type.

Like ``MSO_SHAPE_TYPE.CHART``. Must be implemented by subclasses.

### `top`

```python
top: Length
```

Distance from the top edge of the slide to the top edge of this shape.

Read/write. Expressed in English Metric Units (EMU)

### `width`

```python
width: Length
```

Distance between left and right extents of this shape.

Read/write. Expressed in English Metric Units (EMU).
