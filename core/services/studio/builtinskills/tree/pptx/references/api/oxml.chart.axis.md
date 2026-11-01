<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.chart.axis`

Axis-related oxml objects.

## `BaseAxisElement`

Bases: `BaseOxmlElement`

Base class for catAx, dateAx, valAx, and perhaps other axis elements.

### `defRPr`

```python
defRPr
```

``<a:defRPr>`` great-great-grandchild element, added with its
ancestors if not present.

### `orientation`

```python
orientation
```

Value of `val` attribute of `c:scaling/c:orientation` grandchild element.

Defaults to `ST_Orientation.MIN_MAX` if attribute or any ancestors are not
present.

## `CT_AxisUnit`

Bases: `BaseOxmlElement`

Used for `c:majorUnit` and `c:minorUnit` elements, and others.

### `val`

```python
val = RequiredAttribute('val', ST_AxisUnit)
```

## `CT_CatAx`

Bases: `BaseAxisElement`

`c:catAx` element, defining a category axis.

### `crosses`

```python
crosses = ZeroOrOne('c:crosses', successors=_tag_seq[15:])
```

### `crossesAt`

```python
crossesAt = ZeroOrOne('c:crossesAt', successors=_tag_seq[16:])
```

### `delete_`

```python
delete_ = ZeroOrOne('c:delete', successors=_tag_seq[3:])
```

### `lblOffset`

```python
lblOffset = ZeroOrOne('c:lblOffset', successors=_tag_seq[19:])
```

### `majorGridlines`

```python
majorGridlines = ZeroOrOne('c:majorGridlines', successors=_tag_seq[5:])
```

### `majorTickMark`

```python
majorTickMark = ZeroOrOne('c:majorTickMark', successors=_tag_seq[9:])
```

### `minorGridlines`

```python
minorGridlines = ZeroOrOne('c:minorGridlines', successors=_tag_seq[6:])
```

### `minorTickMark`

```python
minorTickMark = ZeroOrOne('c:minorTickMark', successors=_tag_seq[10:])
```

### `numFmt`

```python
numFmt = ZeroOrOne('c:numFmt', successors=_tag_seq[8:])
```

### `scaling`

```python
scaling = OneAndOnlyOne('c:scaling')
```

### `spPr`

```python
spPr = ZeroOrOne('c:spPr', successors=_tag_seq[12:])
```

### `tickLblPos`

```python
tickLblPos = ZeroOrOne('c:tickLblPos', successors=_tag_seq[11:])
```

### `title`

```python
title = ZeroOrOne('c:title', successors=_tag_seq[7:])
```

### `txPr`

```python
txPr = ZeroOrOne('c:txPr', successors=_tag_seq[13:])
```

## `CT_ChartLines`

Bases: `BaseOxmlElement`

Used for `c:majorGridlines` and `c:minorGridlines`.

Specifies gridlines visual properties such as color and width.

### `spPr`

```python
spPr = ZeroOrOne('c:spPr', successors=())
```

## `CT_Crosses`

Bases: `BaseOxmlElement`

`c:crosses` element, specifying where the other axis crosses this one.

### `val`

```python
val = RequiredAttribute('val', XL_AXIS_CROSSES)
```

## `CT_DateAx`

Bases: `BaseAxisElement`

`c:dateAx` element, defining a date (category) axis.

### `crosses`

```python
crosses = ZeroOrOne('c:crosses', successors=_tag_seq[15:])
```

### `crossesAt`

```python
crossesAt = ZeroOrOne('c:crossesAt', successors=_tag_seq[16:])
```

### `delete_`

```python
delete_ = ZeroOrOne('c:delete', successors=_tag_seq[3:])
```

### `lblOffset`

```python
lblOffset = ZeroOrOne('c:lblOffset', successors=_tag_seq[18:])
```

### `majorGridlines`

```python
majorGridlines = ZeroOrOne('c:majorGridlines', successors=_tag_seq[5:])
```

### `majorTickMark`

```python
majorTickMark = ZeroOrOne('c:majorTickMark', successors=_tag_seq[9:])
```

### `minorGridlines`

```python
minorGridlines = ZeroOrOne('c:minorGridlines', successors=_tag_seq[6:])
```

### `minorTickMark`

```python
minorTickMark = ZeroOrOne('c:minorTickMark', successors=_tag_seq[10:])
```

### `numFmt`

```python
numFmt = ZeroOrOne('c:numFmt', successors=_tag_seq[8:])
```

### `scaling`

```python
scaling = OneAndOnlyOne('c:scaling')
```

### `spPr`

```python
spPr = ZeroOrOne('c:spPr', successors=_tag_seq[12:])
```

### `tickLblPos`

```python
tickLblPos = ZeroOrOne('c:tickLblPos', successors=_tag_seq[11:])
```

### `title`

```python
title = ZeroOrOne('c:title', successors=_tag_seq[7:])
```

### `txPr`

```python
txPr = ZeroOrOne('c:txPr', successors=_tag_seq[13:])
```

## `CT_LblOffset`

Bases: `BaseOxmlElement`

`c:lblOffset` custom element class.

### `val`

```python
val = OptionalAttribute('val', ST_LblOffset, default=100)
```

## `CT_Orientation`

Bases: `BaseOxmlElement`

`c:xAx/c:scaling/c:orientation` element, defining category order.

Used to reverse the order categories appear in on a bar chart so they start at the
top rather than the bottom. Because we read top-to-bottom, the default way looks odd
to many and perhaps most folks. Also applicable to value and date axes.

### `val`

```python
val = OptionalAttribute('val', ST_Orientation, default=ST_Orientation.MIN_MAX)
```

## `CT_Scaling`

Bases: `BaseOxmlElement`

`c:scaling` element.

Defines axis scale characteristics such as maximum value, log vs. linear, etc.

### `max`

```python
max = ZeroOrOne('c:max', successors=_tag_seq[3:])
```

### `maximum`

```python
maximum
```

The float value of the ``<c:max>`` child element, or `None` if no max
element is present.

### `min`

```python
min = ZeroOrOne('c:min', successors=_tag_seq[4:])
```

### `minimum`

```python
minimum
```

The float value of the ``<c:min>`` child element, or `None` if no min
element is present.

### `orientation`

```python
orientation = ZeroOrOne('c:orientation', successors=_tag_seq[2:])
```

## `CT_TickLblPos`

Bases: `BaseOxmlElement`

`c:tickLblPos` element.

### `val`

```python
val = OptionalAttribute('val', XL_TICK_LABEL_POSITION)
```

## `CT_TickMark`

Bases: `BaseOxmlElement`

Used for `c:minorTickMark` and `c:majorTickMark`.

### `val`

```python
val = OptionalAttribute('val', XL_TICK_MARK, default=XL_TICK_MARK.CROSS)
```

## `CT_ValAx`

Bases: `BaseAxisElement`

`c:valAx` element, defining a value axis.

### `crossAx`

```python
crossAx = ZeroOrOne('c:crossAx', successors=_tag_seq[14:])
```

### `crosses`

```python
crosses = ZeroOrOne('c:crosses', successors=_tag_seq[15:])
```

### `crossesAt`

```python
crossesAt = ZeroOrOne('c:crossesAt', successors=_tag_seq[16:])
```

### `delete_`

```python
delete_ = ZeroOrOne('c:delete', successors=_tag_seq[3:])
```

### `majorGridlines`

```python
majorGridlines = ZeroOrOne('c:majorGridlines', successors=_tag_seq[5:])
```

### `majorTickMark`

```python
majorTickMark = ZeroOrOne('c:majorTickMark', successors=_tag_seq[9:])
```

### `majorUnit`

```python
majorUnit = ZeroOrOne('c:majorUnit', successors=_tag_seq[18:])
```

### `minorGridlines`

```python
minorGridlines = ZeroOrOne('c:minorGridlines', successors=_tag_seq[6:])
```

### `minorTickMark`

```python
minorTickMark = ZeroOrOne('c:minorTickMark', successors=_tag_seq[10:])
```

### `minorUnit`

```python
minorUnit = ZeroOrOne('c:minorUnit', successors=_tag_seq[19:])
```

### `numFmt`

```python
numFmt = ZeroOrOne('c:numFmt', successors=_tag_seq[8:])
```

### `scaling`

```python
scaling = OneAndOnlyOne('c:scaling')
```

### `spPr`

```python
spPr = ZeroOrOne('c:spPr', successors=_tag_seq[12:])
```

### `tickLblPos`

```python
tickLblPos = ZeroOrOne('c:tickLblPos', successors=_tag_seq[11:])
```

### `title`

```python
title = ZeroOrOne('c:title', successors=_tag_seq[7:])
```

### `txPr`

```python
txPr = ZeroOrOne('c:txPr', successors=_tag_seq[13:])
```
