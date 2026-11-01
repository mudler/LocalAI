<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.properties`

## `GroupLocking`

```python
GroupLocking(noGrp = None, noUngrp = None, noSelect = None, noRot = None, noChangeAspect = None, noChangeArrowheads = None, noMove = None, noResize = None, noEditPoints = None, noAdjustHandles = None, noChangeShapeType = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `noAdjustHandles`

```python
noAdjustHandles = noAdjustHandles
```

### `noChangeArrowheads`

```python
noChangeArrowheads = noChangeArrowheads
```

### `noChangeAspect`

```python
noChangeAspect = noChangeAspect
```

### `noChangeShapeType`

```python
noChangeShapeType = noChangeShapeType
```

### `noEditPoints`

```python
noEditPoints = noEditPoints
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

### `noRot`

```python
noRot = noRot
```

### `noSelect`

```python
noSelect = noSelect
```

### `noUngrp`

```python
noUngrp = noUngrp
```

### `tagname`

```python
tagname = 'grpSpLocks'
```

## `GroupShapeProperties`

```python
GroupShapeProperties(bwMode = None, xfrm = None, scene3d = None, extLst = None)
```

Bases: `Serialisable`

### `bwMode`

```python
bwMode = bwMode
```

### `extLst`

```python
extLst = extLst
```

### `scene3d`

```python
scene3d = scene3d
```

### `tagname`

```python
tagname = 'grpSpPr'
```

### `xfrm`

```python
xfrm = xfrm
```

## `NonVisualDrawingProps`

```python
NonVisualDrawingProps(id = None, name = None, descr = None, hidden = None, title = None, hlinkClick = None, hlinkHover = None, extLst = None)
```

Bases: `Serialisable`

### `descr`

```python
descr = descr
```

### `extLst`

```python
extLst = extLst
```

### `hidden`

```python
hidden = hidden
```

### `hlinkClick`

```python
hlinkClick = hlinkClick
```

### `hlinkHover`

```python
hlinkHover = hlinkHover
```

### `id`

```python
id = id
```

### `name`

```python
name = name
```

### `tagname`

```python
tagname = 'cNvPr'
```

### `title`

```python
title = title
```

## `NonVisualDrawingShapeProps`

```python
NonVisualDrawingShapeProps(spLocks = None, txBox = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `spLocks`

```python
spLocks = spLocks
```

### `tagname`

```python
tagname = 'cNvSpPr'
```

### `txBax`

```python
txBax = Bool(allow_none=True)
```

### `txBox`

```python
txBox = txBox
```

## `NonVisualGroupDrawingShapeProps`

```python
NonVisualGroupDrawingShapeProps(grpSpLocks = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `grpSpLocks`

```python
grpSpLocks = grpSpLocks
```

### `tagname`

```python
tagname = 'cNvGrpSpPr'
```

## `NonVisualGroupShape`

```python
NonVisualGroupShape(cNvPr = None, cNvGrpSpPr = None)
```

Bases: `Serialisable`

### `cNvGrpSpPr`

```python
cNvGrpSpPr = cNvGrpSpPr
```

### `cNvPr`

```python
cNvPr = cNvPr
```

### `tagname`

```python
tagname = 'nvGrpSpPr'
```
