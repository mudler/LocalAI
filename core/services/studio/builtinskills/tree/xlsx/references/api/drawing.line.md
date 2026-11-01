<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.line`

## `DashStop`

```python
DashStop(d = 0, sp = 0)
```

Bases: `Serialisable`

### `d`

```python
d = d
```

### `length`

```python
length = Alias('d')
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `sp`

```python
sp = sp
```

### `space`

```python
space = Alias('sp')
```

### `tagname`

```python
tagname = 'ds'
```

## `DashStopList`

```python
DashStopList(ds = None)
```

Bases: `Serialisable`

### `ds`

```python
ds = ds
```

## `LineEndProperties`

```python
LineEndProperties(type = None, w = None, len = None)
```

Bases: `Serialisable`

### `len`

```python
len = len
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `tagname`

```python
tagname = 'end'
```

### `type`

```python
type = type
```

### `w`

```python
w = w
```

## `LineProperties`

```python
LineProperties(w = None, cap = None, cmpd = None, algn = None, noFill = None, solidFill = None, gradFill = None, pattFill = None, prstDash = None, custDash = None, round = None, bevel = None, miter = None, headEnd = None, tailEnd = None, extLst = None)
```

Bases: `Serialisable`

### `algn`

```python
algn = algn
```

### `bevel`

```python
bevel = bevel
```

### `cap`

```python
cap = cap
```

### `cmpd`

```python
cmpd = cmpd
```

### `custDash`

```python
custDash = custDash
```

### `dashStyle`

```python
dashStyle = Alias('prstDash')
```

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `gradFill`

```python
gradFill = gradFill
```

### `headEnd`

```python
headEnd = headEnd
```

### `miter`

```python
miter = miter
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `noFill`

```python
noFill = noFill
```

### `pattFill`

```python
pattFill = pattFill
```

### `prstDash`

```python
prstDash = prstDash
```

### `round`

```python
round = round
```

### `solidFill`

```python
solidFill = solidFill
```

### `tagname`

```python
tagname = 'ln'
```

### `tailEnd`

```python
tailEnd = tailEnd
```

### `w`

```python
w = w
```

### `width`

```python
width = Alias('w')
```
