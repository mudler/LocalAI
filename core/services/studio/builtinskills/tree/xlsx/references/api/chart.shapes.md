<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.shapes`

## `GraphicalProperties`

```python
GraphicalProperties(bwMode = None, xfrm = None, noFill = None, solidFill = None, gradFill = None, pattFill = None, ln = None, scene3d = None, custGeom = None, prstGeom = None, sp3d = None, extLst = None)
```

Bases: `Serialisable`

Somewhat vaguely 21.2.2.197 says this:

This element specifies the formatting for the parent chart element. The
custGeom, prstGeom, scene3d, and xfrm elements are not supported. The
bwMode attribute is not supported.

This doesn't leave much. And the element is used in different places.

### `bwMode`

```python
bwMode = bwMode
```

### `custGeom`

```python
custGeom = custGeom
```

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `gradFill`

```python
gradFill = gradFill
```

### `line`

```python
line = Alias('ln')
```

### `ln`

```python
ln = ln
```

### `noFill`

```python
noFill = noFill
```

### `pattFill`

```python
pattFill = pattFill
```

### `prstGeom`

```python
prstGeom = prstGeom
```

### `scene3d`

```python
scene3d = scene3d
```

### `shape3D`

```python
shape3D = Alias('sp3d')
```

### `solidFill`

```python
solidFill = solidFill
```

### `sp3d`

```python
sp3d = sp3d
```

### `tagname`

```python
tagname = 'spPr'
```

### `transform`

```python
transform = Alias('xfrm')
```

### `xfrm`

```python
xfrm = xfrm
```
