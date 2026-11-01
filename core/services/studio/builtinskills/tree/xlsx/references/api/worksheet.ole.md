<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.ole`

## `ObjectAnchor`

```python
ObjectAnchor(_from = None, to = None, moveWithCells = False, sizeWithCells = False, z_order = None)
```

Bases: `Serialisable`

### `moveWithCells`

```python
moveWithCells = moveWithCells
```

### `sizeWithCells`

```python
sizeWithCells = sizeWithCells
```

### `tagname`

```python
tagname = 'anchor'
```

### `to`

```python
to = to
```

### `z_order`

```python
z_order = z_order
```

## `ObjectPr`

```python
ObjectPr(anchor = None, locked = True, defaultSize = True, _print = True, disabled = False, uiObject = False, autoFill = True, autoLine = True, autoPict = True, macro = None, altText = None, dde = False)
```

Bases: `Serialisable`

### `altText`

```python
altText = altText
```

### `anchor`

```python
anchor = anchor
```

### `autoFill`

```python
autoFill = autoFill
```

### `autoLine`

```python
autoLine = autoLine
```

### `autoPict`

```python
autoPict = autoPict
```

### `dde`

```python
dde = dde
```

### `defaultSize`

```python
defaultSize = defaultSize
```

### `disabled`

```python
disabled = disabled
```

### `locked`

```python
locked = locked
```

### `macro`

```python
macro = macro
```

### `tagname`

```python
tagname = 'objectPr'
```

### `uiObject`

```python
uiObject = uiObject
```

## `OleObject`

```python
OleObject(objectPr = None, progId = None, dvAspect = 'DVASPECT_CONTENT', link = None, oleUpdate = None, autoLoad = False, shapeId = None)
```

Bases: `Serialisable`

### `autoLoad`

```python
autoLoad = autoLoad
```

### `dvAspect`

```python
dvAspect = dvAspect
```

### `link`

```python
link = link
```

### `objectPr`

```python
objectPr = objectPr
```

### `oleUpdate`

```python
oleUpdate = oleUpdate
```

### `progId`

```python
progId = progId
```

### `shapeId`

```python
shapeId = shapeId
```

### `tagname`

```python
tagname = 'oleObject'
```

## `OleObjects`

```python
OleObjects(oleObject = ())
```

Bases: `Serialisable`

### `oleObject`

```python
oleObject = oleObject
```

### `tagname`

```python
tagname = 'oleObjects'
```
