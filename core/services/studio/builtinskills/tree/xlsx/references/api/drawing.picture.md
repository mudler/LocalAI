<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.picture`

## `NonVisualPictureProperties`

```python
NonVisualPictureProperties(preferRelativeResize = None, picLocks = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `picLocks`

```python
picLocks = picLocks
```

### `preferRelativeResize`

```python
preferRelativeResize = preferRelativeResize
```

### `tagname`

```python
tagname = 'cNvPicPr'
```

## `PictureFrame`

```python
PictureFrame(macro = None, fPublished = None, nvPicPr = None, blipFill = None, spPr = None, style = None)
```

Bases: `Serialisable`

### `blipFill`

```python
blipFill = blipFill
```

### `fPublished`

```python
fPublished = fPublished
```

### `graphicalProperties`

```python
graphicalProperties = Alias('spPr')
```

### `macro`

```python
macro = macro
```

### `nvPicPr`

```python
nvPicPr = nvPicPr
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
tagname = 'pic'
```

## `PictureLocking`

```python
PictureLocking(noCrop = None, noGrp = None, noSelect = None, noRot = None, noChangeAspect = None, noMove = None, noResize = None, noEditPoints = None, noAdjustHandles = None, noChangeArrowheads = None, noChangeShapeType = None, extLst = None)
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

### `noCrop`

```python
noCrop = noCrop
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

### `tagname`

```python
tagname = 'picLocks'
```

## `PictureNonVisual`

```python
PictureNonVisual(cNvPr = None, cNvPicPr = None)
```

Bases: `Serialisable`

### `cNvPicPr`

```python
cNvPicPr = cNvPicPr
```

### `cNvPr`

```python
cNvPr = cNvPr
```

### `tagname`

```python
tagname = 'nvPicPr'
```
