<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.descriptors.serialisable`

## `KEYWORDS`

```python
KEYWORDS = frozenset(kwlist)
```

## `Serialisable`

Objects can serialise to XML their attributes and child objects.
The following class attributes are created by the metaclass at runtime:
__attrs__ = attributes
__nested__ = single-valued child treated as an attribute
__elements__ = child elements

### `from_tree`

```python
from_tree(node)
```

Create object from XML

### `idx_base`

```python
idx_base = 0
```

### `namespace`

```python
namespace = None
```

### `tagname`

```python
tagname
```

### `to_tree`

```python
to_tree(tagname = None, idx = None, namespace = None)
```

## `seq_types`

```python
seq_types = (list, tuple)
```
