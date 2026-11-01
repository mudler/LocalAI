<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.area_chart`

## `AreaChart`

```python
AreaChart(axId = None, extLst = None, **kw)
```

Bases: `_AreaChartBase`

### `dLbls`

```python
dLbls = _AreaChartBase.dLbls
```

### `dropLines`

```python
dropLines = _AreaChartBase.dropLines
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `grouping`

```python
grouping = _AreaChartBase.grouping
```

### `ser`

```python
ser = _AreaChartBase.ser
```

### `tagname`

```python
tagname = 'areaChart'
```

### `varyColors`

```python
varyColors = _AreaChartBase.varyColors
```

### `x_axis`

```python
x_axis = TextAxis()
```

### `y_axis`

```python
y_axis = NumericAxis()
```

## `AreaChart3D`

```python
AreaChart3D(gapDepth = None, **kw)
```

Bases: `AreaChart`

### `dLbls`

```python
dLbls = _AreaChartBase.dLbls
```

### `dropLines`

```python
dropLines = _AreaChartBase.dropLines
```

### `gapDepth`

```python
gapDepth = gapDepth
```

### `grouping`

```python
grouping = _AreaChartBase.grouping
```

### `ser`

```python
ser = _AreaChartBase.ser
```

### `tagname`

```python
tagname = 'area3DChart'
```

### `varyColors`

```python
varyColors = _AreaChartBase.varyColors
```

### `x_axis`

```python
x_axis = TextAxis()
```

### `y_axis`

```python
y_axis = NumericAxis()
```

### `z_axis`

```python
z_axis = SeriesAxis()
```

## `ChartBase`

```python
ChartBase(axId = (), **kw)
```

Bases: `Serialisable`

Base class for all charts

### `add_data`

```python
add_data(data, from_rows = False, titles_from_data = False)
```

Add a range of data in a single pass.
The default is to treat each column as a data series.

### `anchor`

```python
anchor = 'E15'
```

### `append`

```python
append(value)
```

Append a data series to the chart

### `axId`

```python
axId = axId
```

### `display_blanks`

```python
display_blanks = 'gap'
```

### `graphical_properties`

```python
graphical_properties = None
```

### `height`

```python
height = 7.5
```

### `idx_base`

```python
idx_base = 0
```

### `layout`

```python
layout = None
```

### `legend`

```python
legend = Legend()
```

### `mime_type`

```python
mime_type = 'application/vnd.openxmlformats-officedocument.drawingml.chart+xml'
```

### `path`

```python
path
```

### `pivotFormats`

```python
pivotFormats = ()
```

### `pivotSource`

```python
pivotSource = None
```

### `plot_area`

```python
plot_area = PlotArea()
```

### `repoint`

```python
repoint(series_index, new_range)
```

Point a series' VALUES at ``new_range`` — "the chart now covers
Q1-Q4" (paper-xlsx). ``new_range`` must be a
sheet-qualified single-area range like "'Data'!$B$2:$B$13"; it is
validated here, and under preserve mode the save expresses the
change as a byte patch of the chart's `<c:f>` text.

### `roundedCorners`

```python
roundedCorners = None
```

### `ser`

```python
ser = ()
```

### `series`

```python
series = Alias('ser')
```

### `set_categories`

```python
set_categories(labels)
```

Set the categories / x-axis values

### `style`

```python
style = None
```

### `title`

```python
title = None
```

### `to_tree`

```python
to_tree(namespace = None, tagname = None, idx = None)
```

### `visible_cells_only`

```python
visible_cells_only = True
```

### `width`

```python
width = 15
```
