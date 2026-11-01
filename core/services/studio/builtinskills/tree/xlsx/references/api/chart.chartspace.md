<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.chartspace`

Enclosing chart object. The various chart types are actually child objects.

## `ChartContainer`

```python
ChartContainer(title = None, autoTitleDeleted = None, pivotFmts = (), view3D = None, floor = None, sideWall = None, backWall = None, plotArea = None, legend = None, plotVisOnly = True, dispBlanksAs = 'gap', showDLblsOverMax = None, extLst = None)
```

Bases: `Serialisable`

### `autoTitleDeleted`

```python
autoTitleDeleted = autoTitleDeleted
```

### `backWall`

```python
backWall = backWall
```

### `dispBlanksAs`

```python
dispBlanksAs = dispBlanksAs
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `floor`

```python
floor = floor
```

### `legend`

```python
legend = legend
```

### `pivotFmts`

```python
pivotFmts = pivotFmts
```

### `plotArea`

```python
plotArea = plotArea
```

### `plotVisOnly`

```python
plotVisOnly = plotVisOnly
```

### `showDLblsOverMax`

```python
showDLblsOverMax = showDLblsOverMax
```

### `sideWall`

```python
sideWall = sideWall
```

### `tagname`

```python
tagname = 'chart'
```

### `title`

```python
title = title
```

### `view3D`

```python
view3D = view3D
```

## `ChartSpace`

```python
ChartSpace(date1904 = None, lang = None, roundedCorners = None, style = None, clrMapOvr = None, pivotSource = None, protection = None, chart = None, spPr = None, txPr = None, externalData = None, printSettings = None, userShapes = None, extLst = None)
```

Bases: `Serialisable`

### `chart`

```python
chart = chart
```

### `clrMapOvr`

```python
clrMapOvr = clrMapOvr
```

### `date1904`

```python
date1904 = date1904
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `externalData`

```python
externalData = externalData
```

### `graphical_properties`

```python
graphical_properties = Alias('spPr')
```

### `lang`

```python
lang = lang
```

### `pivotSource`

```python
pivotSource = pivotSource
```

### `printSettings`

```python
printSettings = printSettings
```

### `protection`

```python
protection = protection
```

### `roundedCorners`

```python
roundedCorners = roundedCorners
```

### `spPr`

```python
spPr = spPr
```

### `style`

```python
style = style
```

### `tagname`

```python
tagname = 'chartSpace'
```

### `textProperties`

```python
textProperties = Alias('txPr')
```

### `to_tree`

```python
to_tree(tagname = None, idx = None, namespace = None)
```

### `txPr`

```python
txPr = txPr
```

### `userShapes`

```python
userShapes = userShapes
```

## `ExternalData`

```python
ExternalData(autoUpdate = None, id = None)
```

Bases: `Serialisable`

### `autoUpdate`

```python
autoUpdate = autoUpdate
```

### `id`

```python
id = id
```

### `tagname`

```python
tagname = 'externalData'
```

## `Protection`

```python
Protection(chartObject = None, data = None, formatting = None, selection = None, userInterface = None)
```

Bases: `Serialisable`

### `chartObject`

```python
chartObject = chartObject
```

### `data`

```python
data = data
```

### `formatting`

```python
formatting = formatting
```

### `selection`

```python
selection = selection
```

### `tagname`

```python
tagname = 'protection'
```

### `userInterface`

```python
userInterface = userInterface
```
