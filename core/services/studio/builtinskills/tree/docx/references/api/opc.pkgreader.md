<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.pkgreader`

Low-level, read-only API to a serialized Open Packaging Convention (OPC) package.

## `PackageReader`

```python
PackageReader(content_types, pkg_srels, sparts)
```

Provides access to the contents of a zip-format OPC package via its
`serialized_parts` and `pkg_srels` attributes.

### `from_file`

```python
from_file(pkg_file)
```

A `PackageReader` loaded from `pkg_file`, with the package graph already validated.

This is the read path behind `Document()`, so a malformed file is caught here rather
than carried into the object model. Raises `MalformedPackageError` for a corrupt or
ambiguous archive, a relationship targeting a missing part, a part with no declared
content type, or a content type that contradicts its relationship.

### `iter_sparts`

```python
iter_sparts()
```

Generate a 4-tuple `(partname, content_type, reltype, blob)` for each of the
serialized parts in the package.

### `iter_srels`

```python
iter_srels()
```

Generate a 2-tuple `(source_uri, srel)` for each of the relationships in the
package.

## `content_type_matches`

```python
content_type_matches(actual: str, expected: str) -> bool
```

## `is_relationship_type`

```python
is_relationship_type(actual: str, expected: str) -> bool
```

## `is_xml_id`

```python
is_xml_id(value: object) -> bool
```

Whether ``value`` has the XML Schema ID/NCName lexical form.
