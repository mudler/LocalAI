<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.ns`

Namespace related objects.

## `NamespacePrefixedTag`

```python
NamespacePrefixedTag(nstag: str)
```

Bases: `str`

Value object that knows the semantics of an XML tag having a namespace prefix.

### `clark_name`

```python
clark_name
```

### `from_clark_name`

```python
from_clark_name(clark_name: str) -> NamespacePrefixedTag
```

### `local_part`

```python
local_part
```

Return the local part of the tag as a string. E.g. 'foobar' is
returned for tag 'f:foobar'.

### `nsmap`

```python
nsmap
```

Return a dict having a single member, mapping the namespace prefix of
this tag to it's namespace name (e.g. {'f': 'http://foo/bar'}). This
is handy for passing to xpath calls and other uses.

### `nspfx`

```python
nspfx
```

Return the string namespace prefix for the tag, e.g. 'f' is returned
for tag 'f:foobar'.

### `nsuri`

```python
nsuri
```

Return the namespace URI for the tag, e.g. 'http://foo/bar' would be
returned for tag 'f:foobar' if the 'f' prefix maps to
'http://foo/bar' in _nsmap.

## `namespaces`

```python
namespaces(*prefixes: str)
```

Return a dict containing the subset namespace prefix mappings specified by *prefixes*.

Any number of namespace prefixes can be supplied, e.g. namespaces('a', 'r', 'p').

## `nsdecls`

```python
nsdecls(*prefixes: str)
```

## `nsmap`

```python
nsmap = namespaces
```

## `nsuri`

```python
nsuri(nspfx: str)
```

Return the namespace URI corresponding to `nspfx`.

Example:

    >>> nsuri("p")
    "http://schemas.openxmlformats.org/presentationml/2006/main"

## `pfxmap`

```python
pfxmap = {value: key for key, value in _nsmap.items()}
```

## `qn`

```python
qn(namespace_prefixed_tag: str) -> str
```

Return a Clark-notation qualified tag name corresponding to `namespace_prefixed_tag`.

`namespace_prefixed_tag` is a string like 'p:body'. 'qn' stands for `qualified name`.

As an example, `qn("p:cSld")` returns:
    `"{http://schemas.openxmlformats.org/drawingml/2006/main}cSld"`.
