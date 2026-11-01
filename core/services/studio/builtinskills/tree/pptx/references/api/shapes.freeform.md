<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes.freeform`

Objects related to construction of freeform shapes.

## `CT_DrawingOperation`

```python
CT_DrawingOperation: TypeAlias = 'CT_Path2DClose | CT_Path2DLineTo | CT_Path2DMoveTo'
```

## `DrawingOperation`

```python
DrawingOperation: TypeAlias = '_LineSegment | _MoveTo | _Close'
```

## `FreeformBuilder`

```python
FreeformBuilder(shapes: _BaseGroupShapes, start_x: Length, start_y: Length, x_scale: float, y_scale: float)
```

Bases: `Sequence[DrawingOperation]`

Allows a freeform shape to be specified and created.

The initial pen position is provided on construction. From there, drawing proceeds using
successive calls to draw line segments. The freeform shape may be closed by calling the
`close` method.

A shape may have more than one contour, in which case overlapping areas are "subtracted". A
contour is a sequence of line segments beginning with a "move-to" operation. A move-to
operation is automatically inserted in each new freeform; additional move-to ops can be
inserted with the `.move_to()` method.

### `add_line_segments`

```python
add_line_segments(vertices: Iterable[tuple[float, float]], close: bool = True)
```

Add a straight line segment to each point in `vertices`.

`vertices` must be an iterable of (x, y) pairs (2-tuples). Each x and y value is rounded
to the nearest integer before use. The optional `close` parameter determines whether the
resulting contour is `closed` or left `open`.

Returns this `FreeformBuilder` object so it can be used in chained calls.

### `convert_to_shape`

```python
convert_to_shape(origin_x: Length = Emu(0), origin_y: Length = Emu(0))
```

Return new freeform shape positioned relative to specified offset.

`origin_x` and `origin_y` locate the origin of the local coordinate system in slide
coordinates (EMU), perhaps most conveniently by use of a `Length` object.

Note that this method may be called more than once to add multiple shapes of the same
geometry in different locations on the slide.

### `move_to`

```python
move_to(x: float, y: float)
```

Move pen to (x, y) (local coordinates) without drawing line.

Returns this `FreeformBuilder` object so it can be used in chained calls.

### `new`

```python
new(shapes: _BaseGroupShapes, start_x: float, start_y: float, x_scale: float, y_scale: float)
```

Return a new `FreeformBuilder` object.

The initial pen location is specified (in local coordinates) by
(`start_x`, `start_y`).

### `shape_offset_x`

```python
shape_offset_x: Length
```

Return x distance of shape origin from local coordinate origin.

The returned integer represents the leftmost extent of the freeform shape, in local
coordinates. Note that the bounding box of the shape need not start at the local origin.

### `shape_offset_y`

```python
shape_offset_y: Length
```

Return y distance of shape origin from local coordinate origin.

The returned integer represents the topmost extent of the freeform shape, in local
coordinates. Note that the bounding box of the shape need not start at the local origin.
