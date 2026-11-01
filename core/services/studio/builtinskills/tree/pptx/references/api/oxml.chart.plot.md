<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.chart.plot`

Plot-related oxml objects.

## `BaseChartElement`

Bases: `BaseOxmlElement`

Base class for barChart, lineChart, and other plot elements.

### `cat`

```python
cat
```

Return the `c:cat` element of the first series in this xChart, or
`None` if not present.

### `cat_pt_count`

```python
cat_pt_count
```

Return the value of the `c:ptCount` descendent of this xChart
element. Its parent can be one of three element types. This value
represents the true number of (leaf) categories, although they might
not all have a corresponding `c:pt` sibling; a category with no label
does not get a `c:pt` element. Returns 0 if there is no `c:ptCount`
descendent.

### `cat_pts`

```python
cat_pts
```

Return a sequence representing the `c:pt` elements under the `c:cat`
element of the first series in this xChart element. A category having
no value will have no corresponding `c:pt` element; `None` will
appear in that position in such cases. Items appear in `idx` order.
Only those in the first ``<c:lvl>`` element are included in the case
of multi-level categories.

### `grouping_val`

```python
grouping_val
```

Return the value of the ``./c:grouping{val=?}`` attribute, taking
defaults into account when items are not present.

### `iter_sers`

```python
iter_sers()
```

Generate each ``<c:ser>`` child element in this xChart in
c:order/@val sequence (not document or c:idx order).

### `sers`

```python
sers
```

Sequence of ``<c:ser>`` child elements in this xChart in c:order/@val
sequence (not document or c:idx order).

## `CT_Area3DChart`

Bases: `BaseChartElement`

``<c:area3DChart>`` element.

### `grouping`

```python
grouping = ZeroOrOne('c:grouping', successors=('c:varyColors', 'c:ser', 'c:dLbls', 'c:dropLines', 'c:gapDepth', 'c:axId'))
```

## `CT_AreaChart`

Bases: `BaseChartElement`

``<c:areaChart>`` element.

### `dLbls`

```python
dLbls = ZeroOrOne('c:dLbls', successors=_tag_seq[4:])
```

### `grouping`

```python
grouping = ZeroOrOne('c:grouping', successors=_tag_seq[1:])
```

### `ser`

```python
ser = ZeroOrMore('c:ser', successors=_tag_seq[3:])
```

### `varyColors`

```python
varyColors = ZeroOrOne('c:varyColors', successors=_tag_seq[2:])
```

## `CT_BarChart`

Bases: `BaseChartElement`

``<c:barChart>`` element.

### `barDir`

```python
barDir = OneAndOnlyOne('c:barDir')
```

### `dLbls`

```python
dLbls = ZeroOrOne('c:dLbls', successors=_tag_seq[5:])
```

### `gapWidth`

```python
gapWidth = ZeroOrOne('c:gapWidth', successors=_tag_seq[6:])
```

### `grouping`

```python
grouping = ZeroOrOne('c:grouping', successors=_tag_seq[2:])
```

### `grouping_val`

```python
grouping_val
```

Return the value of the ``./c:grouping{val=?}`` attribute, taking
defaults into account when items are not present.

### `overlap`

```python
overlap = ZeroOrOne('c:overlap', successors=_tag_seq[7:])
```

### `ser`

```python
ser = ZeroOrMore('c:ser', successors=_tag_seq[4:])
```

### `varyColors`

```python
varyColors = ZeroOrOne('c:varyColors', successors=_tag_seq[3:])
```

## `CT_BarDir`

Bases: `BaseOxmlElement`

``<c:barDir>`` child of a barChart element, specifying the orientation of
the bars, 'bar' if they are horizontal and 'col' if they are vertical.

### `val`

```python
val = OptionalAttribute('val', ST_BarDir, default=ST_BarDir.COL)
```

## `CT_BubbleChart`

Bases: `BaseChartElement`

``<c:bubbleChart>`` custom element class

### `bubble3D`

```python
bubble3D = ZeroOrOne('c:bubble3D', successors=_tag_seq[5:])
```

### `bubbleScale`

```python
bubbleScale = ZeroOrOne('c:bubbleScale', successors=_tag_seq[6:])
```

### `dLbls`

```python
dLbls = ZeroOrOne('c:dLbls', successors=_tag_seq[3:])
```

### `ser`

```python
ser = ZeroOrMore('c:ser', successors=_tag_seq[2:])
```

## `CT_BubbleScale`

Bases: `BaseChartElement`

``<c:bubbleScale>`` custom element class

### `val`

```python
val = OptionalAttribute('val', ST_BubbleScale, default=100)
```

## `CT_DoughnutChart`

Bases: `BaseChartElement`

``<c:doughnutChart>`` element.

### `dLbls`

```python
dLbls = ZeroOrOne('c:dLbls', successors=_tag_seq[3:])
```

### `ser`

```python
ser = ZeroOrMore('c:ser', successors=_tag_seq[2:])
```

### `varyColors`

```python
varyColors = ZeroOrOne('c:varyColors', successors=_tag_seq[1:])
```

## `CT_GapAmount`

Bases: `BaseOxmlElement`

``<c:gapWidth>`` child of ``<c:barChart>`` element, also used for other
purposes like error bars.

### `val`

```python
val = OptionalAttribute('val', ST_GapAmount, default=150)
```

## `CT_Grouping`

Bases: `BaseOxmlElement`

``<c:grouping>`` child of an xChart element, specifying a value like
'clustered' or 'stacked'. Also used for variants with the same tag name
like CT_BarGrouping.

### `val`

```python
val = OptionalAttribute('val', ST_Grouping)
```

## `CT_LineChart`

Bases: `BaseChartElement`

``<c:lineChart>`` custom element class

### `dLbls`

```python
dLbls = ZeroOrOne('c:dLbls', successors=_tag_seq[4:])
```

### `grouping`

```python
grouping = ZeroOrOne('c:grouping', successors=_tag_seq[1:])
```

### `ser`

```python
ser = ZeroOrMore('c:ser', successors=_tag_seq[3:])
```

### `varyColors`

```python
varyColors = ZeroOrOne('c:varyColors', successors=_tag_seq[2:])
```

## `CT_Overlap`

Bases: `BaseOxmlElement`

``<c:overlap>`` element specifying bar overlap as an integer percentage
of bar width, in range -100 to 100.

### `val`

```python
val = OptionalAttribute('val', ST_Overlap, default=0)
```

## `CT_PieChart`

Bases: `BaseChartElement`

``<c:pieChart>`` custom element class

### `dLbls`

```python
dLbls = ZeroOrOne('c:dLbls', successors=_tag_seq[3:])
```

### `ser`

```python
ser = ZeroOrMore('c:ser', successors=_tag_seq[2:])
```

### `varyColors`

```python
varyColors = ZeroOrOne('c:varyColors', successors=_tag_seq[1:])
```

## `CT_RadarChart`

Bases: `BaseChartElement`

``<c:radarChart>`` custom element class

### `dLbls`

```python
dLbls = ZeroOrOne('c:dLbls', successors=_tag_seq[4:])
```

### `ser`

```python
ser = ZeroOrMore('c:ser', successors=_tag_seq[3:])
```

### `varyColors`

```python
varyColors = ZeroOrOne('c:varyColors', successors=_tag_seq[2:])
```

## `CT_ScatterChart`

Bases: `BaseChartElement`

``<c:scatterChart>`` custom element class

### `ser`

```python
ser = ZeroOrMore('c:ser', successors=_tag_seq[3:])
```

### `varyColors`

```python
varyColors = ZeroOrOne('c:varyColors', successors=_tag_seq[2:])
```
