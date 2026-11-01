<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.packaging.custom`

Implementation of custom properties see § 22.3 in the specification

## `BoolProperty`

Bases: `_TypedProperty`

### `value`

```python
value = Bool()
```

## `CLASS_MAPPING`

```python
CLASS_MAPPING = {StringProperty: 'lpwstr', IntProperty: 'i4', FloatProperty: 'r8', DateTimeProperty: 'filetime', BoolProperty: 'bool', LinkProperty: 'linkTarget'}
```

## `CustomPropertyList`

```python
CustomPropertyList()
```

Bases: `Strict`

### `append`

```python
append(prop)
```

### `from_tree`

```python
from_tree(tree)
```

Create list from OOXML element

### `names`

```python
names
```

List of property names

### `props`

```python
props = []
```

### `to_tree`

```python
to_tree()
```

## `DateTimeProperty`

Bases: `_TypedProperty`

### `value`

```python
value = DateTime()
```

## `FloatProperty`

Bases: `_TypedProperty`

### `value`

```python
value = Float()
```

## `IntProperty`

Bases: `_TypedProperty`

### `value`

```python
value = Integer()
```

## `LinkProperty`

Bases: `_TypedProperty`

### `value`

```python
value = String()
```

## `NestedBoolText`

Bases: `Bool`, `NestedText`

Descriptor for handling nested elements with the value stored in the text part

## `StringProperty`

Bases: `_TypedProperty`

### `value`

```python
value = String(allow_none=True)
```

## `XML_MAPPING`

```python
XML_MAPPING = {v: k for k, v in CLASS_MAPPING.items()}
```
