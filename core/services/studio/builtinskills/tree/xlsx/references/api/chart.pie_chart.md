<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.pie_chart`

## `CustomSplit`

```python
CustomSplit(secondPiePt = ())
```

Bases: `Serialisable`

### `secondPiePt`

```python
secondPiePt = secondPiePt
```

### `tagname`

```python
tagname = 'custSplit'
```

## `DoughnutChart`

```python
DoughnutChart(firstSliceAng = 0, holeSize = 10, extLst = None, **kw)
```

Bases: `_PieChartBase`

### `dLbls`

```python
dLbls = _PieChartBase.dLbls
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `firstSliceAng`

```python
firstSliceAng = firstSliceAng
```

### `holeSize`

```python
holeSize = holeSize
```

### `ser`

```python
ser = _PieChartBase.ser
```

### `tagname`

```python
tagname = 'doughnutChart'
```

### `varyColors`

```python
varyColors = _PieChartBase.varyColors
```

## `PieChart`

```python
PieChart(firstSliceAng = 0, extLst = None, **kw)
```

Bases: `_PieChartBase`

### `dLbls`

```python
dLbls = _PieChartBase.dLbls
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `firstSliceAng`

```python
firstSliceAng = firstSliceAng
```

### `ser`

```python
ser = _PieChartBase.ser
```

### `tagname`

```python
tagname = 'pieChart'
```

### `varyColors`

```python
varyColors = _PieChartBase.varyColors
```

## `PieChart3D`

Bases: `_PieChartBase`

### `dLbls`

```python
dLbls = _PieChartBase.dLbls
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `ser`

```python
ser = _PieChartBase.ser
```

### `tagname`

```python
tagname = 'pie3DChart'
```

### `varyColors`

```python
varyColors = _PieChartBase.varyColors
```

## `ProjectedPieChart`

```python
ProjectedPieChart(ofPieType = 'pie', gapWidth = None, splitType = 'auto', splitPos = None, custSplit = None, secondPieSize = 75, serLines = None, extLst = None, **kw)
```

Bases: `_PieChartBase`

From the spec 21.2.2.126

This element contains the pie of pie or bar of pie series on this
chart. Only the first series shall be displayed. The splitType element
shall determine whether the splitPos and custSplit elements apply.

### `custSplit`

```python
custSplit = custSplit
```

### `dLbls`

```python
dLbls = _PieChartBase.dLbls
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `gapWidth`

```python
gapWidth = gapWidth
```

### `join_lines`

```python
join_lines = Alias('serLines')
```

### `ofPieType`

```python
ofPieType = ofPieType
```

### `secondPieSize`

```python
secondPieSize = secondPieSize
```

### `ser`

```python
ser = _PieChartBase.ser
```

### `serLines`

```python
serLines = Typed(expected_type=ChartLines, allow_none=True)
```

### `splitPos`

```python
splitPos = splitPos
```

### `splitType`

```python
splitType = splitType
```

### `tagname`

```python
tagname = 'ofPieChart'
```

### `type`

```python
type = Alias('ofPieType')
```

### `varyColors`

```python
varyColors = _PieChartBase.varyColors
```
