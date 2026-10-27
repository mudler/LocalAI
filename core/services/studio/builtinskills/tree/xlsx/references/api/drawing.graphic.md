<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.graphic`

## `GraphicData`

```python
GraphicData(uri = CHART_NS, chart = None)
```

Bases: `Serialisable`

### `chart`

```python
chart = chart
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `tagname`

```python
tagname = 'graphicData'
```

### `uri`

```python
uri = uri
```

## `GraphicFrame`

```python
GraphicFrame(nvGraphicFramePr = None, xfrm = None, graphic = None, macro = None, fPublished = None)
```

Bases: `Serialisable`

### `fPublished`

```python
fPublished = fPublished
```

### `graphic`

```python
graphic = graphic
```

### `macro`

```python
macro = macro
```

### `nvGraphicFramePr`

```python
nvGraphicFramePr = nvGraphicFramePr
```

### `tagname`

```python
tagname = 'graphicFrame'
```

### `xfrm`

```python
xfrm = xfrm
```

## `GraphicFrameLocking`

```python
GraphicFrameLocking(noGrp = None, noDrilldown = None, noSelect = None, noChangeAspect = None, noMove = None, noResize = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = extLst
```

### `noChangeAspect`

```python
noChangeAspect = noChangeAspect
```

### `noDrilldown`

```python
noDrilldown = noDrilldown
```

### `noGrp`

```python
noGrp = noGrp
```

### `noMove`

```python
noMove = noMove
```

### `noResize`

```python
noResize = noResize
```

### `noSelect`

```python
noSelect = noSelect
```

## `GraphicObject`

```python
GraphicObject(graphicData = None)
```

Bases: `Serialisable`

### `graphicData`

```python
graphicData = graphicData
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `tagname`

```python
tagname = 'graphic'
```

## `GroupShape`

```python
GroupShape(nvGrpSpPr = None, grpSpPr = None, pic = None)
```

Bases: `Serialisable`

### `grpSpPr`

```python
grpSpPr = grpSpPr
```

### `nonVisualProperties`

```python
nonVisualProperties = Alias('nvGrpSpPr')
```

### `nvGrpSpPr`

```python
nvGrpSpPr = nvGrpSpPr
```

### `pic`

```python
pic = pic
```

### `visualProperties`

```python
visualProperties = Alias('grpSpPr')
```

## `NonVisualGraphicFrame`

```python
NonVisualGraphicFrame(cNvPr = None, cNvGraphicFramePr = None)
```

Bases: `Serialisable`

### `cNvGraphicFramePr`

```python
cNvGraphicFramePr = cNvGraphicFramePr
```

### `cNvPr`

```python
cNvPr = cNvPr
```

### `tagname`

```python
tagname = 'nvGraphicFramePr'
```

## `NonVisualGraphicFrameProperties`

```python
NonVisualGraphicFrameProperties(graphicFrameLocks = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = extLst
```

### `graphicFrameLocks`

```python
graphicFrameLocks = graphicFrameLocks
```

### `tagname`

```python
tagname = 'cNvGraphicFramePr'
```
