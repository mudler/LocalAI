<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.data_source`

Collection of utility primitives for charts.

## `AxDataSource`

```python
AxDataSource(numRef = None, numLit = None, strRef = None, strLit = None, multiLvlStrRef = None)
```

Bases: `Serialisable`

### `multiLvlStrRef`

```python
multiLvlStrRef = multiLvlStrRef
```

### `numLit`

```python
numLit = numLit
```

### `numRef`

```python
numRef = numRef
```

### `strLit`

```python
strLit = strLit
```

### `strRef`

```python
strRef = strRef
```

### `tagname`

```python
tagname = 'cat'
```

## `Level`

```python
Level(pt = ())
```

Bases: `Serialisable`

### `pt`

```python
pt = pt
```

### `tagname`

```python
tagname = 'lvl'
```

## `MultiLevelStrData`

```python
MultiLevelStrData(ptCount = None, lvl = (), extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `lvl`

```python
lvl = lvl
```

### `ptCount`

```python
ptCount = ptCount
```

### `tagname`

```python
tagname = 'multiLvlStrData'
```

## `MultiLevelStrRef`

```python
MultiLevelStrRef(f = None, multiLvlStrCache = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `f`

```python
f = f
```

### `multiLvlStrCache`

```python
multiLvlStrCache = multiLvlStrCache
```

### `tagname`

```python
tagname = 'multiLvlStrRef'
```

## `NumData`

```python
NumData(formatCode = None, ptCount = None, pt = (), extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `formatCode`

```python
formatCode = formatCode
```

### `pt`

```python
pt = pt
```

### `ptCount`

```python
ptCount = ptCount
```

## `NumDataSource`

```python
NumDataSource(numRef = None, numLit = None)
```

Bases: `Serialisable`

### `numLit`

```python
numLit = numLit
```

### `numRef`

```python
numRef = numRef
```

## `NumFmt`

```python
NumFmt(formatCode = None, sourceLinked = False)
```

Bases: `Serialisable`

### `formatCode`

```python
formatCode = formatCode
```

### `sourceLinked`

```python
sourceLinked = sourceLinked
```

## `NumRef`

```python
NumRef(f = None, numCache = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `f`

```python
f = f
```

### `numCache`

```python
numCache = numCache
```

### `ref`

```python
ref = Alias('f')
```

## `NumVal`

```python
NumVal(idx = None, formatCode = None, v = None)
```

Bases: `Serialisable`

### `formatCode`

```python
formatCode = formatCode
```

### `idx`

```python
idx = idx
```

### `v`

```python
v = v
```

## `NumberValueDescriptor`

Bases: `NestedText`

Data should be numerical but isn't always :-/

### `allow_none`

```python
allow_none = True
```

## `StrData`

```python
StrData(ptCount = None, pt = (), extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `pt`

```python
pt = pt
```

### `ptCount`

```python
ptCount = ptCount
```

### `tagname`

```python
tagname = 'strData'
```

## `StrRef`

```python
StrRef(f = None, strCache = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `f`

```python
f = f
```

### `strCache`

```python
strCache = strCache
```

### `tagname`

```python
tagname = 'strRef'
```

## `StrVal`

```python
StrVal(idx = 0, v = None)
```

Bases: `Serialisable`

### `idx`

```python
idx = idx
```

### `tagname`

```python
tagname = 'strVal'
```

### `v`

```python
v = v
```
