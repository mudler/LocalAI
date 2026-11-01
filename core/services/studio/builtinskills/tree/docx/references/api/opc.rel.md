<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.rel`

Relationship-related objects.

## `Relationships`

```python
Relationships(baseURI: str)
```

Bases: `Dict[str, '_Relationship']`

Collection object for `_Relationship` instances, having list semantics.

### `add_relationship`

```python
add_relationship(reltype: str, target: Part | str, rId: str, is_external: bool = False) -> '_Relationship'
```

Return a newly added `_Relationship` instance.

### `get_or_add`

```python
get_or_add(reltype: str, target_part: Part) -> _Relationship
```

Return relationship of `reltype` to `target_part`, newly added if not already
present in collection.

### `get_or_add_ext_rel`

```python
get_or_add_ext_rel(reltype: str, target_ref: str) -> str
```

Return rId of external relationship of `reltype` to `target_ref`, newly added
if not already present in collection.

### `part_with_reltype`

```python
part_with_reltype(reltype: str) -> Part
```

Return target part of rel with matching `reltype`, raising `KeyError` if not
found and `ValueError` if more than one matching relationship is found.

### `related_parts`

```python
related_parts
```

Dict mapping rIds to target parts for all the internal relationships in the
collection.

### `xml`

```python
xml: str
```

Serialize this relationship collection into XML suitable for storage as a
.rels file in an OPC package.
