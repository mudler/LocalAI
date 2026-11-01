<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.ns`

Namespace-related objects.

## `NamespacePrefixedTag`

```python
NamespacePrefixedTag(nstag: str)
```

Bases: `str`

Value object that knows the semantics of an XML tag having a namespace prefix.

### `clark_name`

```python
clark_name: str
```

### `from_clark_name`

```python
from_clark_name(clark_name: str) -> NamespacePrefixedTag
```

### `local_part`

```python
local_part: str
```

The local part of this tag.

E.g. "foobar" is returned for tag "f:foobar".

### `nsmap`

```python
nsmap: Dict[str, str]
```

Single-member dict mapping prefix of this tag to it's namespace name.

Example: `{"f": "http://foo/bar"}`. This is handy for passing to xpath calls
and other uses.

### `nspfx`

```python
nspfx: str
```

The namespace-prefix for this tag.

For example, "f" is returned for tag "f:foobar".

### `nsuri`

```python
nsuri: str
```

The namespace URI for this tag.

For example, "http://foo/bar" would be returned for tag "f:foobar" if the "f"
prefix maps to "http://foo/bar" in nsmap.

## `nsdecls`

```python
nsdecls(*prefixes: str) -> str
```

Namespace declaration including each namespace-prefix in `prefixes`.

Handy for adding required namespace declarations to a tree root element.

## `nsmap`

```python
nsmap = {'a': 'http://schemas.openxmlformats.org/drawingml/2006/main', 'c': 'http://schemas.openxmlformats.org/drawingml/2006/chart', 'cp': 'http://schemas.openxmlformats.org/package/2006/metadata/core-properties', 'dc': 'http://purl.org/dc/elements/1.1/', 'dcmitype': 'http://purl.org/dc/dcmitype/', 'dcterms': 'http://purl.org/dc/terms/', 'dgm': 'http://schemas.openxmlformats.org/drawingml/2006/diagram', 'm': 'http://schemas.openxmlformats.org/officeDocument/2006/math', 'pic': 'http://schemas.openxmlformats.org/drawingml/2006/picture', 'r': 'http://schemas.openxmlformats.org/officeDocument/2006/relationships', 'sl': 'http://schemas.openxmlformats.org/schemaLibrary/2006/main', 'w': 'http://schemas.openxmlformats.org/wordprocessingml/2006/main', 'w14': 'http://schemas.microsoft.com/office/word/2010/wordml', 'wp': 'http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing', 'xml': 'http://www.w3.org/XML/1998/namespace', 'xsi': 'http://www.w3.org/2001/XMLSchema-instance'}
```

## `nspfxmap`

```python
nspfxmap(*nspfxs: str) -> Dict[str, str]
```

Subset namespace-prefix mappings specified by *nspfxs*.

Any number of namespace prefixes can be supplied, e.g. namespaces("a", "r", "p").

## `pfxmap`

```python
pfxmap = {value: key for key, value in nsmap.items()}
```

## `qn`

```python
qn(tag: str) -> str
```

Stands for "qualified name".

This utility function converts a familiar namespace-prefixed tag name like "w:p"
into a Clark-notation qualified tag name for lxml. For example, `qn("w:p")` returns
"{http://schemas.openxmlformats.org/wordprocessingml/2006/main}p".
