<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.pagebreak`

## `Break`

```python
Break(id = 0, min = 0, max = 16383, man = True, pt = None)
```

Bases: `Serialisable`

### `id`

```python
id = id
```

### `man`

```python
man = man
```

### `max`

```python
max = max
```

### `min`

```python
min = min
```

### `pt`

```python
pt = pt
```

### `tagname`

```python
tagname = 'brk'
```

## `ColBreak`

Bases: `RowBreak`

### `brk`

```python
brk = RowBreak.brk
```

### `count`

```python
count = RowBreak.count
```

### `manualBreakCount`

```python
manualBreakCount = RowBreak.manualBreakCount
```

### `tagname`

```python
tagname = 'colBreaks'
```

## `PageBreak`

```python
PageBreak = RowBreak
```

## `RowBreak`

```python
RowBreak(count = None, manualBreakCount = None, brk = ())
```

Bases: `Serialisable`

### `append`

```python
append(brk = None)
```

Add a page break

### `brk`

```python
brk = brk
```

### `count`

```python
count
```

### `manualBreakCount`

```python
manualBreakCount
```

### `tagname`

```python
tagname = 'rowBreaks'
```
