<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.styles.differential`

## `DifferentialStyle`

```python
DifferentialStyle(font = None, numFmt = None, fill = None, alignment = None, border = None, protection = None, extLst = None)
```

Bases: `Serialisable`

### `alignment`

```python
alignment = alignment
```

### `border`

```python
border = border
```

### `extLst`

```python
extLst = extLst
```

### `fill`

```python
fill = fill
```

### `font`

```python
font = font
```

### `numFmt`

```python
numFmt = numFmt
```

### `protection`

```python
protection = protection
```

### `tagname`

```python
tagname = 'dxf'
```

## `DifferentialStyleList`

```python
DifferentialStyleList(dxf = (), count = None)
```

Bases: `Serialisable`

Dedupable container for differential styles.

### `add`

```python
add(dxf)
```

Add a differential style and return its index

### `append`

```python
append(dxf)
```

Check to see whether style already exists and append it if does not.

### `count`

```python
count
```

### `dxf`

```python
dxf = dxf
```

### `styles`

```python
styles = Alias('dxf')
```

### `tagname`

```python
tagname = 'dxfs'
```
