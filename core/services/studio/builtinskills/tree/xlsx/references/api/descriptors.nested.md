<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.descriptors.nested`

Generic serialisable classes

## `EmptyTag`

Bases: `Nested`, `Bool`

Boolean if a tag exists or not.

### `from_tree`

```python
from_tree(node)
```

### `to_tree`

```python
to_tree(tagname = None, value = None, namespace = None)
```

## `Nested`

Bases: `Descriptor`

### `attribute`

```python
attribute = 'val'
```

### `from_tree`

```python
from_tree(node)
```

### `nested`

```python
nested = True
```

### `to_tree`

```python
to_tree(tagname = None, value = None, namespace = None)
```

## `NestedBool`

Bases: `NestedValue`, `Bool`

### `from_tree`

```python
from_tree(node)
```

## `NestedFloat`

Bases: `NestedValue`, `Float`

## `NestedInteger`

Bases: `NestedValue`, `Integer`

## `NestedMinMax`

Bases: `Nested`, `MinMax`

## `NestedNoneSet`

Bases: `Nested`, `NoneSet`

## `NestedSet`

Bases: `Nested`, `Set`

## `NestedString`

Bases: `NestedValue`, `String`

## `NestedText`

Bases: `NestedValue`

Represents any nested tag with the value as the contents of the tag

### `from_tree`

```python
from_tree(node)
```

### `to_tree`

```python
to_tree(tagname = None, value = None, namespace = None)
```

## `NestedValue`

Bases: `Nested`, `Convertible`

Nested tag storing the value on the 'val' attribute
