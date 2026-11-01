<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.connector`

## `Connection`

```python
Connection(id = None, idx = None)
```

Bases: `Serialisable`

### `id`

```python
id = id
```

### `idx`

```python
idx = idx
```

## `ConnectorLocking`

```python
ConnectorLocking(extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = extLst
```

## `ConnectorNonVisual`

```python
ConnectorNonVisual(cNvPr = None, cNvCxnSpPr = None)
```

Bases: `Serialisable`

### `cNvCxnSpPr`

```python
cNvCxnSpPr = cNvCxnSpPr
```

### `cNvPr`

```python
cNvPr = cNvPr
```

## `ConnectorShape`

```python
ConnectorShape(nvCxnSpPr = None, spPr = None, style = None, macro = None, fPublished = None)
```

Bases: `Serialisable`

### `fPublished`

```python
fPublished = fPublished
```

### `macro`

```python
macro = macro
```

### `nvCxnSpPr`

```python
nvCxnSpPr = nvCxnSpPr
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
tagname = 'cxnSp'
```

## `NonVisualConnectorProperties`

```python
NonVisualConnectorProperties(cxnSpLocks = None, stCxn = None, endCxn = None, extLst = None)
```

Bases: `Serialisable`

### `cxnSpLocks`

```python
cxnSpLocks = cxnSpLocks
```

### `endCxn`

```python
endCxn = endCxn
```

### `extLst`

```python
extLst = extLst
```

### `stCxn`

```python
stCxn = stCxn
```

## `Shape`

```python
Shape(macro = None, textlink = None, fPublished = None, fLocksText = None, nvSpPr = None, spPr = None, style = None, txBody = None)
```

Bases: `Serialisable`

### `fLocksText`

```python
fLocksText = fLocksText
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

### `meta`

```python
meta = Alias('nvSpPr')
```

### `nvSpPr`

```python
nvSpPr = nvSpPr
```

### `spPr`

```python
spPr = spPr
```

### `style`

```python
style = style
```

### `textlink`

```python
textlink = textlink
```

### `txBody`

```python
txBody = txBody
```

## `ShapeMeta`

```python
ShapeMeta(cNvPr = None, cNvSpPr = None)
```

Bases: `Serialisable`

### `cNvPr`

```python
cNvPr = cNvPr
```

### `cNvSpPr`

```python
cNvSpPr = cNvSpPr
```

### `tagname`

```python
tagname = 'nvSpPr'
```
