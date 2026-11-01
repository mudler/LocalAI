<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes.connector`

Connector (line) shape and related objects.

## `Connector`

Bases: `BaseShape`

Connector (line) shape.

A connector is a linear shape having end-points that can be connected to
other objects (but not to other connectors). A connector can be straight,
have elbows, or can be curved.

### `begin_connect`

```python
begin_connect(shape, cxn_pt_idx)
```

**EXPERIMENTAL** - *The current implementation only works properly
with rectangular shapes, such as pictures and rectangles. Use with
other shape types may cause unexpected visual alignment of the
connected end-point and could lead to a load error if cxn_pt_idx
exceeds the connection point count available on the connected shape.
That said, a quick test should reveal what to expect when using this
method with other shape types.*

Connect the beginning of this connector to *shape* at the connection
point specified by *cxn_pt_idx*. Each shape has zero or more
connection points and they are identified by index, starting with 0.
Generally, the first connection point of a shape is at the top center
of its bounding box and numbering proceeds counter-clockwise from
there. However this is only a convention and may vary, especially
with non built-in shapes.

### `begin_x`

```python
begin_x
```

Return the X-position of the begin point of this connector, in
English Metric Units (as a `Length` object).

### `begin_y`

```python
begin_y
```

Return the Y-position of the begin point of this connector, in
English Metric Units (as a `Length` object).

### `end_connect`

```python
end_connect(shape, cxn_pt_idx)
```

**EXPERIMENTAL** - *The current implementation only works properly
with rectangular shapes, such as pictures and rectangles. Use with
other shape types may cause unexpected visual alignment of the
connected end-point and could lead to a load error if cxn_pt_idx
exceeds the connection point count available on the connected shape.
That said, a quick test should reveal what to expect when using this
method with other shape types.*

Connect the ending of this connector to *shape* at the connection
point specified by *cxn_pt_idx*.

### `end_x`

```python
end_x
```

Return the X-position of the end point of this connector, in English
Metric Units (as a `Length` object).

### `end_y`

```python
end_y
```

Return the Y-position of the end point of this connector, in English
Metric Units (as a `Length` object).

### `get_or_add_ln`

```python
get_or_add_ln()
```

Helper method required by `LineFormat`.

### `line`

```python
line()
```

`LineFormat` instance for this connector.

Provides access to line properties such as line color, width, and
line style.

### `ln`

```python
ln
```

Helper method required by `LineFormat`.

The ``<a:ln>`` element containing the line format properties such as
line color and width. `None` if no `<a:ln>` element is present.

### `shape_type`

```python
shape_type
```

Member of `MSO_SHAPE_TYPE` identifying the type of this shape.

Unconditionally `MSO_SHAPE_TYPE.LINE` for a `Connector` object.
