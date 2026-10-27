<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.descriptors.sequence`

## `MultiSequence`

Bases: `Sequence`

Sequences can contain objects with different tags

### `to_tree`

```python
to_tree(tagname, obj, namespace = None)
```

Convert the sequence represented by the descriptor to an XML element

## `MultiSequencePart`

```python
MultiSequencePart(expected_type, store)
```

Bases: `Alias`

Allow a multisequence to be built up from parts

Excluded from the instance __elements__ or __attrs__ as is effectively an Alias

### `expected_type`

```python
expected_type = expected_type
```

### `store`

```python
store = store
```

## `NestedSequence`

Bases: `Sequence`

Wrap a sequence in an containing object

### `count`

```python
count = False
```

### `from_tree`

```python
from_tree(node)
```

### `to_tree`

```python
to_tree(tagname, obj, namespace = None)
```

## `Sequence`

Bases: `Descriptor`

A sequence (list or tuple) that may only contain objects of the declared
type

### `container`

```python
container = list
```

### `expected_type`

```python
expected_type = type(None)
```

### `idx_base`

```python
idx_base = 0
```

### `seq_types`

```python
seq_types = (list, tuple)
```

### `to_tree`

```python
to_tree(tagname, obj, namespace = None)
```

Convert the sequence represented by the descriptor to an XML element

### `unique`

```python
unique = False
```

## `UniqueSequence`

Bases: `Sequence`

Use a set to keep values unique

### `container`

```python
container = set
```

### `seq_types`

```python
seq_types = (list, tuple, set)
```

## `ValueSequence`

Bases: `Sequence`

A sequence of primitive types that are stored as a single attribute.
"val" is the default attribute

### `attribute`

```python
attribute = 'val'
```

### `from_tree`

```python
from_tree(node)
```

### `to_tree`

```python
to_tree(tagname, obj, namespace = None)
```
