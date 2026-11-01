<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.plotarea`

## `DataTable`

```python
DataTable(showHorzBorder = None, showVertBorder = None, showOutline = None, showKeys = None, spPr = None, txPr = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `graphicalProperties`

```python
graphicalProperties = Alias('spPr')
```

### `showHorzBorder`

```python
showHorzBorder = showHorzBorder
```

### `showKeys`

```python
showKeys = showKeys
```

### `showOutline`

```python
showOutline = showOutline
```

### `showVertBorder`

```python
showVertBorder = showVertBorder
```

### `spPr`

```python
spPr = spPr
```

### `tagname`

```python
tagname = 'dTable'
```

### `txPr`

```python
txPr = txPr
```

## `PlotArea`

```python
PlotArea(layout = None, dTable = None, spPr = None, _charts = (), _axes = (), extLst = None)
```

Bases: `Serialisable`

### `area3DChart`

```python
area3DChart = MultiSequencePart(expected_type=AreaChart3D, store='_charts')
```

### `areaChart`

```python
areaChart = MultiSequencePart(expected_type=AreaChart, store='_charts')
```

### `bar3DChart`

```python
bar3DChart = MultiSequencePart(expected_type=BarChart3D, store='_charts')
```

### `barChart`

```python
barChart = MultiSequencePart(expected_type=BarChart, store='_charts')
```

### `bubbleChart`

```python
bubbleChart = MultiSequencePart(expected_type=BubbleChart, store='_charts')
```

### `catAx`

```python
catAx = MultiSequencePart(expected_type=TextAxis, store='_axes')
```

### `dTable`

```python
dTable = dTable
```

### `dateAx`

```python
dateAx = MultiSequencePart(expected_type=DateAxis, store='_axes')
```

### `doughnutChart`

```python
doughnutChart = MultiSequencePart(expected_type=DoughnutChart, store='_charts')
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `from_tree`

```python
from_tree(node)
```

### `graphicalProperties`

```python
graphicalProperties = Alias('spPr')
```

### `layout`

```python
layout = layout
```

### `line3DChart`

```python
line3DChart = MultiSequencePart(expected_type=LineChart3D, store='_charts')
```

### `lineChart`

```python
lineChart = MultiSequencePart(expected_type=LineChart, store='_charts')
```

### `ofPieChart`

```python
ofPieChart = MultiSequencePart(expected_type=ProjectedPieChart, store='_charts')
```

### `pie3DChart`

```python
pie3DChart = MultiSequencePart(expected_type=PieChart3D, store='_charts')
```

### `pieChart`

```python
pieChart = MultiSequencePart(expected_type=PieChart, store='_charts')
```

### `radarChart`

```python
radarChart = MultiSequencePart(expected_type=RadarChart, store='_charts')
```

### `scatterChart`

```python
scatterChart = MultiSequencePart(expected_type=ScatterChart, store='_charts')
```

### `serAx`

```python
serAx = MultiSequencePart(expected_type=SeriesAxis, store='_axes')
```

### `spPr`

```python
spPr = spPr
```

### `stockChart`

```python
stockChart = MultiSequencePart(expected_type=StockChart, store='_charts')
```

### `surface3DChart`

```python
surface3DChart = MultiSequencePart(expected_type=SurfaceChart3D, store='_charts')
```

### `surfaceChart`

```python
surfaceChart = MultiSequencePart(expected_type=SurfaceChart, store='_charts')
```

### `tagname`

```python
tagname = 'plotArea'
```

### `to_tree`

```python
to_tree(tagname = None, idx = None, namespace = None)
```

### `valAx`

```python
valAx = MultiSequencePart(expected_type=NumericAxis, store='_axes')
```
