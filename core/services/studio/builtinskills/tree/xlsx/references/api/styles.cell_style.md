<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.styles.cell_style`

## `ArrayDescriptor`

```python
ArrayDescriptor(key)
```

### `key`

```python
key = key
```

## `CellStyle`

```python
CellStyle(numFmtId = 0, fontId = 0, fillId = 0, borderId = 0, xfId = None, quotePrefix = None, pivotButton = None, applyNumberFormat = None, applyFont = None, applyFill = None, applyBorder = None, applyAlignment = None, applyProtection = None, alignment = None, protection = None, extLst = None)
```

Bases: `Serialisable`

### `alignment`

```python
alignment = alignment
```

### `applyAlignment`

```python
applyAlignment
```

### `applyBorder`

```python
applyBorder = applyBorder
```

### `applyFill`

```python
applyFill = applyFill
```

### `applyFont`

```python
applyFont = applyFont
```

### `applyNumberFormat`

```python
applyNumberFormat = applyNumberFormat
```

### `applyProtection`

```python
applyProtection
```

### `borderId`

```python
borderId = borderId
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `fillId`

```python
fillId = fillId
```

### `fontId`

```python
fontId = fontId
```

### `from_array`

```python
from_array(style)
```

Convert from StyleArray

### `numFmtId`

```python
numFmtId = numFmtId
```

### `pivotButton`

```python
pivotButton = pivotButton
```

### `protection`

```python
protection = protection
```

### `quotePrefix`

```python
quotePrefix = quotePrefix
```

### `tagname`

```python
tagname = 'xf'
```

### `to_array`

```python
to_array()
```

Convert to StyleArray

### `xfId`

```python
xfId = xfId
```

## `CellStyleList`

```python
CellStyleList(count = None, xf = ())
```

Bases: `Serialisable`

### `alignment`

```python
alignment = Sequence(expected_type=Alignment)
```

### `count`

```python
count
```

### `protection`

```python
protection = Sequence(expected_type=Protection)
```

### `tagname`

```python
tagname = 'cellXfs'
```

### `xf`

```python
xf = xf
```

## `StyleArray`

Bases: `array`

Simplified named tuple with an array

### `alignmentId`

```python
alignmentId = ArrayDescriptor(5)
```

### `borderId`

```python
borderId = ArrayDescriptor(2)
```

### `fillId`

```python
fillId = ArrayDescriptor(1)
```

### `fontId`

```python
fontId = ArrayDescriptor(0)
```

### `numFmtId`

```python
numFmtId = ArrayDescriptor(3)
```

### `pivotButton`

```python
pivotButton = ArrayDescriptor(6)
```

### `protectionId`

```python
protectionId = ArrayDescriptor(4)
```

### `quotePrefix`

```python
quotePrefix = ArrayDescriptor(7)
```

### `tagname`

```python
tagname = 'xf'
```

### `xfId`

```python
xfId = ArrayDescriptor(8)
```
