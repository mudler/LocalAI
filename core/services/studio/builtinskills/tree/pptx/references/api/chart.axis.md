<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.axis`

Axis-related chart objects.

## `AxisTitle`

```python
AxisTitle(title)
```

Bases: `ElementProxy`

Provides properties for manipulating axis title.

### `format`

```python
format()
```

`ChartFormat` object providing access to shape formatting.

Return the `ChartFormat` object providing shape formatting properties
for this axis title, such as its line color and fill.

### `has_text_frame`

```python
has_text_frame
```

Read/write Boolean specifying presence of a text frame.

Return `True` if this axis title has a text frame, and `False`
otherwise. Assigning `True` causes a text frame to be added if not
already present. Assigning `False` causes any existing text frame to
be removed along with any text contained in the text frame.

### `text_frame`

```python
text_frame
```

`TextFrame` instance for this axis title.

Return a `TextFrame` instance allowing read/write access to the text
of this axis title and its text formatting properties. Accessing this
property is destructive as it adds a new text frame if not already
present.

## `CategoryAxis`

Bases: `_BaseAxis`

A category axis of a chart.

### `category_type`

```python
category_type
```

A member of `XlCategoryType` specifying the scale type of this
axis. Unconditionally ``CATEGORY_SCALE`` for a `CategoryAxis` object.

## `DateAxis`

Bases: `_BaseAxis`

A category axis with dates as its category labels.

This axis-type has some special display behaviors such as making length of equal
periods equal and normalizing month start dates despite unequal month lengths.

### `category_type`

```python
category_type
```

A member of `XlCategoryType` specifying the scale type of this
axis. Unconditionally ``TIME_SCALE`` for a `DateAxis` object.

## `MajorGridlines`

```python
MajorGridlines(xAx)
```

Bases: `ElementProxy`

Provides access to the properties of the major gridlines appearing on an axis.

### `format`

```python
format()
```

The `ChartFormat` object providing access to the shape formatting
properties of this data point, such as line and fill.

## `TickLabels`

```python
TickLabels(xAx_elm)
```

Bases: `object`

A service class providing access to formatting of axis tick mark labels.

### `font`

```python
font()
```

The `Font` object that provides access to the text properties for
these tick labels, such as bold, italic, etc.

### `number_format`

```python
number_format
```

Read/write string (e.g. "$#,##0.00") specifying the format for the
numbers on this axis. The syntax for these strings is the same as it
appears in the PowerPoint or Excel UI. Returns 'General' if no number
format has been set. Note that this format string has no effect on
rendered tick labels when `number_format_is_linked` is `True`.
Assigning a format string to this property automatically sets
`number_format_is_linked` to `False`.

### `number_format_is_linked`

```python
number_format_is_linked
```

Read/write boolean specifying whether number formatting should be
taken from the source spreadsheet rather than the value of
`number_format`.

### `offset`

```python
offset
```

Read/write int value in range 0-1000 specifying the spacing between
the tick mark labels and the axis as a percentange of the default
value. 100 if no label offset setting is present.

## `ValueAxis`

Bases: `_BaseAxis`

An axis having continuous (as opposed to discrete) values.

The vertical axis is generally a value axis, however both axes of an XY-type chart
are value axes.

### `crosses`

```python
crosses
```

Member of `XlAxisCrosses` enumeration specifying the point on
this axis where the other axis crosses, such as auto/zero, minimum,
or maximum. Returns `XL_AXIS_CROSSES.CUSTOM` when a specific numeric
crossing point (e.g. 1.5) is defined.

### `crosses_at`

```python
crosses_at
```

Numeric value on this axis at which the perpendicular axis crosses.
Returns `None` if no crossing value is set.

### `major_unit`

```python
major_unit
```

The float number of units between major tick marks on this value
axis. `None` corresponds to the 'Auto' setting in the UI, and
specifies the value should be calculated by PowerPoint based on the
underlying chart data.

### `minor_unit`

```python
minor_unit
```

The float number of units between minor tick marks on this value
axis. `None` corresponds to the 'Auto' setting in the UI, and
specifies the value should be calculated by PowerPoint based on the
underlying chart data.
