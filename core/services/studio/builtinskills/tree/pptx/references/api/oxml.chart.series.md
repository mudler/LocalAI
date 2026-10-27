<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.chart.series`

Series-related oxml objects.

## `CT_AxDataSource`

Bases: `BaseOxmlElement`

``<c:cat>`` custom element class used in category charts to specify
category labels and hierarchy.

### `lvls`

```python
lvls
```

Return a list containing the `c:lvl` descendent elements in document
order. These will only be present when the required single child
is a `c:multiLvlStrRef` element. Returns an empty list when no
`c:lvl` descendent elements are present.

### `multiLvlStrRef`

```python
multiLvlStrRef = ZeroOrOne('c:multiLvlStrRef', successors=())
```

## `CT_DPt`

Bases: `BaseOxmlElement`

``<c:dPt>`` custom element class, containing visual properties for a data
point.

### `idx`

```python
idx = OneAndOnlyOne('c:idx')
```

### `marker`

```python
marker = ZeroOrOne('c:marker', successors=_tag_seq[3:])
```

### `new_dPt`

```python
new_dPt()
```

Return a newly created "loose" `c:dPt` element containing its default
subtree.

### `spPr`

```python
spPr = ZeroOrOne('c:spPr', successors=_tag_seq[6:])
```

## `CT_Lvl`

Bases: `BaseOxmlElement`

``<c:lvl>`` custom element class used in multi-level categories to
specify a level of hierarchy.

### `pt`

```python
pt = ZeroOrMore('c:pt', successors=())
```

## `CT_NumDataSource`

Bases: `BaseOxmlElement`

``<c:yVal>`` custom element class used in XY and bubble charts, and
perhaps others.

### `numRef`

```python
numRef = OneAndOnlyOne('c:numRef')
```

### `ptCount_val`

```python
ptCount_val
```

Return the value of `./c:numRef/c:numCache/c:ptCount/@val`,
specifying how many `c:pt` elements are in this numeric data cache.
Returns 0 if no `c:ptCount` element is present, as this is the least
disruptive way to degrade when no cached point data is available.
This situation is not expected, but is valid according to the schema.

### `pt_v`

```python
pt_v(idx)
```

Return the Y value for data point *idx* in this cache, or None if no
value is present for that data point.

## `CT_SeriesComposite`

Bases: `BaseOxmlElement`

``<c:ser>`` custom element class. Note there are several different series
element types in the schema, such as ``CT_LineSer`` and ``CT_BarSer``,
but they all share the same tag name. This class acts as a composite and
depends on the caller not to do anything invalid for a series belonging
to a particular plot type.

### `bubbleSize`

```python
bubbleSize = ZeroOrOne('c:bubbleSize', successors=_tag_seq[19:])
```

### `bubbleSize_ptCount_val`

```python
bubbleSize_ptCount_val
```

Return the number of bubble size values as reflected in the `val`
attribute of `./c:bubbleSize//c:ptCount`, or 0 if not present.

### `cat`

```python
cat = ZeroOrOne('c:cat', successors=_tag_seq[13:])
```

### `cat_ptCount_val`

```python
cat_ptCount_val
```

Return the number of categories as reflected in the `val` attribute
of `./c:cat//c:ptCount`, or 0 if not present.

### `dLbls`

```python
dLbls = ZeroOrOne('c:dLbls', successors=_tag_seq[10:])
```

### `dPt`

```python
dPt = ZeroOrMore('c:dPt', successors=_tag_seq[9:])
```

### `get_dLbl`

```python
get_dLbl(idx)
```

Return the `c:dLbl` element representing the label for the data point
at offset *idx* in this series, or `None` if not present.

### `get_or_add_dLbl`

```python
get_or_add_dLbl(idx)
```

Return the `c:dLbl` element representing the label of the point at
offset *idx* in this series, newly created if not yet present.

### `get_or_add_dPt_for_point`

```python
get_or_add_dPt_for_point(idx)
```

Return the `c:dPt` child representing the visual properties of the
data point at index *idx*.

### `idx`

```python
idx = OneAndOnlyOne('c:idx')
```

### `invertIfNegative`

```python
invertIfNegative = ZeroOrOne('c:invertIfNegative', successors=_tag_seq[5:])
```

### `marker`

```python
marker = ZeroOrOne('c:marker', successors=_tag_seq[7:])
```

### `order`

```python
order = OneAndOnlyOne('c:order')
```

### `smooth`

```python
smooth = ZeroOrOne('c:smooth', successors=_tag_seq[18:])
```

### `spPr`

```python
spPr = ZeroOrOne('c:spPr', successors=_tag_seq[4:])
```

### `tx`

```python
tx = ZeroOrOne('c:tx', successors=_tag_seq[3:])
```

### `val`

```python
val = ZeroOrOne('c:val', successors=_tag_seq[14:])
```

### `xVal`

```python
xVal = ZeroOrOne('c:xVal', successors=_tag_seq[15:])
```

### `xVal_ptCount_val`

```python
xVal_ptCount_val
```

Return the number of X values as reflected in the `val` attribute of
`./c:xVal//c:ptCount`, or 0 if not present.

### `yVal`

```python
yVal = ZeroOrOne('c:yVal', successors=_tag_seq[16:])
```

### `yVal_ptCount_val`

```python
yVal_ptCount_val
```

Return the number of Y values as reflected in the `val` attribute of
`./c:yVal//c:ptCount`, or 0 if not present.

## `CT_StrVal_NumVal_Composite`

Bases: `BaseOxmlElement`

``<c:pt>`` element, can be either CT_StrVal or CT_NumVal complex type.
Using this class for both, differentiating as needed.

### `idx`

```python
idx = RequiredAttribute('idx', XsdUnsignedInt)
```

### `v`

```python
v = OneAndOnlyOne('c:v')
```

### `value`

```python
value
```

The float value of the text in the required ``<c:v>`` child.
