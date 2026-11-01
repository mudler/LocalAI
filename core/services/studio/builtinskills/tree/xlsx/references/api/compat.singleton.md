<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.compat.singleton`

## `Cached`

```python
Cached(*args, **kw)
```

Bases: `type`

Caching metaclass
Child classes will only create new instances of themselves if
one doesn't already exist. Does not work with __slots__

## `Singleton`

```python
Singleton(*args, **kw)
```

Bases: `type`

Singleton metaclass
Based on Python Cookbook 3rd Edition Recipe 9.13
Only one instance of a class can exist. Does not work with __slots__
