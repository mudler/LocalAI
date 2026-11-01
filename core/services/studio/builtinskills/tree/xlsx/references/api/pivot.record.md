<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.pivot.record`

## `Record`

```python
Record(_fields = (), m = None, n = None, b = None, e = None, s = None, d = None, x = None)
```

Bases: `Serialisable`

### `b`

```python
b = MultiSequencePart(expected_type=Boolean, store='_fields')
```

### `d`

```python
d = MultiSequencePart(expected_type=DateTimeField, store='_fields')
```

### `e`

```python
e = MultiSequencePart(expected_type=Error, store='_fields')
```

### `m`

```python
m = MultiSequencePart(expected_type=Missing, store='_fields')
```

### `n`

```python
n = MultiSequencePart(expected_type=Number, store='_fields')
```

### `s`

```python
s = MultiSequencePart(expected_type=Text, store='_fields')
```

### `tagname`

```python
tagname = 'r'
```

### `x`

```python
x = MultiSequencePart(expected_type=Index, store='_fields')
```

## `RecordList`

```python
RecordList(count = None, r = (), extLst = None)
```

Bases: `Serialisable`

### `count`

```python
count
```

### `extLst`

```python
extLst = extLst
```

### `mime_type`

```python
mime_type = 'application/vnd.openxmlformats-officedocument.spreadsheetml.pivotCacheRecords+xml'
```

### `path`

```python
path
```

### `r`

```python
r = r
```

### `rel_type`

```python
rel_type = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships/pivotCacheRecords'
```

### `tagname`

```python
tagname = 'pivotCacheRecords'
```

### `to_tree`

```python
to_tree()
```
