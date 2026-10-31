<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.shared`

Objects shared by opc modules.

## `CaseInsensitiveDict`

Bases: `Dict[str, Any]`

Mapping type that behaves like dict except that it matches without respect to the
case of the key.

E.g. cid['A'] == cid['a']. Note this is not general-purpose, just complete enough to
satisfy opc package needs. It assumes str keys, and that it is created empty; keys
passed in constructor are not accounted for

## `cls_method_fn`

```python
cls_method_fn(cls: type, method_name: str)
```

Return method of `cls` having `method_name`.
