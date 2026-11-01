<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.utils.escape`

OOXML has non-standard escaping for characters < 

## `escape`

```python
escape(value)
```

Convert ASCII < 31 to OOXML: \n == _x + hex(ord(\n)) + _

## `unescape`

```python
unescape(value)
```

Convert escaped strings to ASCIII: _x000a_ == \n
