<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.series`

## `Series`

```python
Series(idx = 0, order = 0, tx = None, spPr = None, pictureOptions = None, dPt = (), dLbls = None, trendline = None, errBars = None, cat = None, val = None, invertIfNegative = None, shape = None, xVal = None, yVal = None, bubbleSize = None, bubble3D = None, marker = None, smooth = None, explosion = None, extLst = None)
```

Bases: `Serialisable`

Generic series object. Should not be instantiated directly.
User the chart.Series factory instead.

### `bubble3D`

```python
bubble3D = bubble3D
```

### `bubbleSize`

```python
bubbleSize = bubbleSize
```

### `cat`

```python
cat = cat
```

### `dLbls`

```python
dLbls = dLbls
```

### `dPt`

```python
dPt = dPt
```

### `data_points`

```python
data_points = Alias('dPt')
```

### `errBars`

```python
errBars = errBars
```

### `explosion`

```python
explosion = explosion
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `graphicalProperties`

```python
graphicalProperties = Alias('spPr')
```

### `identifiers`

```python
identifiers = Alias('cat')
```

### `idx`

```python
idx = idx
```

### `invertIfNegative`

```python
invertIfNegative = invertIfNegative
```

### `labels`

```python
labels = Alias('dLbls')
```

### `marker`

```python
marker = marker
```

### `order`

```python
order = order
```

### `pictureOptions`

```python
pictureOptions = pictureOptions
```

### `shape`

```python
shape = shape
```

### `smooth`

```python
smooth = smooth
```

### `spPr`

```python
spPr = spPr
```

### `tagname`

```python
tagname = 'ser'
```

### `title`

```python
title = Alias('tx')
```

### `to_tree`

```python
to_tree(tagname = None, idx = None)
```

The index can need rebasing

### `trendline`

```python
trendline = trendline
```

### `tx`

```python
tx = tx
```

### `val`

```python
val = val
```

### `xVal`

```python
xVal = xVal
```

### `yVal`

```python
yVal = yVal
```

### `zVal`

```python
zVal = Alias('bubbleSize')
```

## `SeriesLabel`

```python
SeriesLabel(strRef = None, v = None)
```

Bases: `Serialisable`

### `strRef`

```python
strRef = strRef
```

### `tagname`

```python
tagname = 'tx'
```

### `v`

```python
v = v
```

### `value`

```python
value = Alias('v')
```

## `XYSeries`

Bases: `Series`

Dedicated series for charts that have x and y series

### `bubble3D`

```python
bubble3D = Series.bubble3D
```

### `bubbleSize`

```python
bubbleSize = Series.bubbleSize
```

### `dLbls`

```python
dLbls = Series.dLbls
```

### `dPt`

```python
dPt = Series.dPt
```

### `errBars`

```python
errBars = Series.errBars
```

### `idx`

```python
idx = Series.idx
```

### `invertIfNegative`

```python
invertIfNegative = Series.invertIfNegative
```

### `marker`

```python
marker = Series.marker
```

### `order`

```python
order = Series.order
```

### `smooth`

```python
smooth = Series.smooth
```

### `spPr`

```python
spPr = Series.spPr
```

### `trendline`

```python
trendline = Series.trendline
```

### `tx`

```python
tx = Series.tx
```

### `xVal`

```python
xVal = Series.xVal
```

### `yVal`

```python
yVal = Series.yVal
```

## `attribute_mapping`

```python
attribute_mapping = {'area': ('idx', 'order', 'tx', 'spPr', 'pictureOptions', 'dPt', 'dLbls', 'errBars', 'trendline', 'cat', 'val'), 'bar': ('idx', 'order', 'tx', 'spPr', 'invertIfNegative', 'pictureOptions', 'dPt', 'dLbls', 'trendline', 'errBars', 'cat', 'val', 'shape'), 'bubble': ('idx', 'order', 'tx', 'spPr', 'invertIfNegative', 'dPt', 'dLbls', 'trendline', 'errBars', 'xVal', 'yVal', 'bubbleSize', 'bubble3D'), 'line': ('idx', 'order', 'tx', 'spPr', 'marker', 'dPt', 'dLbls', 'trendline', 'errBars', 'cat', 'val', 'smooth'), 'pie': ('idx', 'order', 'tx', 'spPr', 'explosion', 'dPt', 'dLbls', 'cat', 'val'), 'radar': ('idx', 'order', 'tx', 'spPr', 'marker', 'dPt', 'dLbls', 'cat', 'val'), 'scatter': ('idx', 'order', 'tx', 'spPr', 'marker', 'dPt', 'dLbls', 'trendline', 'errBars', 'xVal', 'yVal', 'smooth'), 'surface': ('idx', 'order', 'tx', 'spPr', 'cat', 'val')}
```
