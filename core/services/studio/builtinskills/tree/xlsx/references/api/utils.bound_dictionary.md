<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.utils.bound_dictionary`

## `BoundDictionary`

```python
BoundDictionary(reference = None, *args, **kw)
```

Bases: `defaultdict`

A default dictionary where elements are tightly coupled.

The factory method is responsible for binding the parent object to the child.

If a reference attribute is assigned then child objects will have the key assigned to this.

Otherwise it's just a defaultdict.

### `reference`

```python
reference = reference
```
