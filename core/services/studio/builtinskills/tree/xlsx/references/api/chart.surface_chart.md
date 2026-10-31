<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.surface_chart`

## `BandFormat`

```python
BandFormat(idx = 0, spPr = None)
```

Bases: `Serialisable`

### `graphicalProperties`

```python
graphicalProperties = Alias('spPr')
```

### `idx`

```python
idx = idx
```

### `spPr`

```python
spPr = spPr
```

### `tagname`

```python
tagname = 'bandFmt'
```

## `BandFormatList`

```python
BandFormatList(bandFmt = ())
```

Bases: `Serialisable`

### `bandFmt`

```python
bandFmt = bandFmt
```

### `tagname`

```python
tagname = 'bandFmts'
```

## `SurfaceChart`

```python
SurfaceChart(**kw)
```

Bases: `SurfaceChart3D`

### `bandFmts`

```python
bandFmts = _SurfaceChartBase.bandFmts
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `ser`

```python
ser = _SurfaceChartBase.ser
```

### `tagname`

```python
tagname = 'surfaceChart'
```

### `wireframe`

```python
wireframe = _SurfaceChartBase.wireframe
```

## `SurfaceChart3D`

```python
SurfaceChart3D(**kw)
```

Bases: `_SurfaceChartBase`, `_3DBase`

### `bandFmts`

```python
bandFmts = _SurfaceChartBase.bandFmts
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `ser`

```python
ser = _SurfaceChartBase.ser
```

### `tagname`

```python
tagname = 'surface3DChart'
```

### `wireframe`

```python
wireframe = _SurfaceChartBase.wireframe
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
