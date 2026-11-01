<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.spreadsheet_drawing`

## `AbsoluteAnchor`

```python
AbsoluteAnchor(pos = None, ext = None, **kw)
```

Bases: `_AnchorBase`

### `clientData`

```python
clientData = _AnchorBase.clientData
```

### `contentPart`

```python
contentPart = _AnchorBase.contentPart
```

### `cxnSp`

```python
cxnSp = _AnchorBase.cxnSp
```

### `ext`

```python
ext = ext
```

### `graphicFrame`

```python
graphicFrame = _AnchorBase.graphicFrame
```

### `grpSp`

```python
grpSp = _AnchorBase.grpSp
```

### `pic`

```python
pic = _AnchorBase.pic
```

### `pos`

```python
pos = pos
```

### `sp`

```python
sp = _AnchorBase.sp
```

### `tagname`

```python
tagname = 'absoluteAnchor'
```

## `AnchorClientData`

```python
AnchorClientData(fLocksWithSheet = None, fPrintsWithSheet = None)
```

Bases: `Serialisable`

### `fLocksWithSheet`

```python
fLocksWithSheet = fLocksWithSheet
```

### `fPrintsWithSheet`

```python
fPrintsWithSheet = fPrintsWithSheet
```

## `AnchorMarker`

```python
AnchorMarker(col = 0, colOff = 0, row = 0, rowOff = 0)
```

Bases: `Serialisable`

### `col`

```python
col = col
```

### `colOff`

```python
colOff = colOff
```

### `row`

```python
row = row
```

### `rowOff`

```python
rowOff = rowOff
```

### `tagname`

```python
tagname = 'marker'
```

## `OneCellAnchor`

```python
OneCellAnchor(_from = None, ext = None, **kw)
```

Bases: `_AnchorBase`

### `clientData`

```python
clientData = _AnchorBase.clientData
```

### `contentPart`

```python
contentPart = _AnchorBase.contentPart
```

### `cxnSp`

```python
cxnSp = _AnchorBase.cxnSp
```

### `ext`

```python
ext = ext
```

### `graphicFrame`

```python
graphicFrame = _AnchorBase.graphicFrame
```

### `grpSp`

```python
grpSp = _AnchorBase.grpSp
```

### `pic`

```python
pic = _AnchorBase.pic
```

### `sp`

```python
sp = _AnchorBase.sp
```

### `tagname`

```python
tagname = 'oneCellAnchor'
```

## `SpreadsheetDrawing`

```python
SpreadsheetDrawing(twoCellAnchor = (), oneCellAnchor = (), absoluteAnchor = ())
```

Bases: `Serialisable`

### `PartName`

```python
PartName = '/xl/drawings/drawing{0}.xml'
```

### `absoluteAnchor`

```python
absoluteAnchor = absoluteAnchor
```

### `charts`

```python
charts = []
```

### `images`

```python
images = []
```

### `mime_type`

```python
mime_type = 'application/vnd.openxmlformats-officedocument.drawing+xml'
```

### `oneCellAnchor`

```python
oneCellAnchor = oneCellAnchor
```

### `path`

```python
path
```

### `tagname`

```python
tagname = 'wsDr'
```

### `twoCellAnchor`

```python
twoCellAnchor = twoCellAnchor
```

## `TwoCellAnchor`

```python
TwoCellAnchor(editAs = None, _from = None, to = None, **kw)
```

Bases: `_AnchorBase`

### `clientData`

```python
clientData = _AnchorBase.clientData
```

### `contentPart`

```python
contentPart = _AnchorBase.contentPart
```

### `cxnSp`

```python
cxnSp = _AnchorBase.cxnSp
```

### `editAs`

```python
editAs = editAs
```

### `graphicFrame`

```python
graphicFrame = _AnchorBase.graphicFrame
```

### `grpSp`

```python
grpSp = _AnchorBase.grpSp
```

### `pic`

```python
pic = _AnchorBase.pic
```

### `sp`

```python
sp = _AnchorBase.sp
```

### `tagname`

```python
tagname = 'twoCellAnchor'
```

### `to`

```python
to = to
```
