<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.opc.serialized`

API for reading/writing serialized Open Packaging Convention (OPC) package.

## `GuardedZipReader`

```python
GuardedZipReader(zip_file: ZipFile)
```

Validate and stream every member of an already-open ``ZipFile``.

Construction fully validates the archive and caches every member's bytes. This makes an ordinary
``Presentation()`` open share the same validation, including for members not reachable from OPC
relationships.

Wrap `zip_file`, splitting its records into all members and the subset that can be OPC
parts.

### `order`

```python
order: Tuple[str, ...]
```

Member names in central-directory order.

### `read`

```python
read(name: str) -> bytes
```

Return validated bytes for member `name`.

### `read_all`

```python
read_all() -> Tuple[Dict[str, bytes], List[str]]
```

Return a copy of validated parts and their archive order.

## `PackageReader`

```python
PackageReader(pkg_file: str | IO[bytes])
```

Bases: `Container[bytes]`

Provides access to package-parts of an OPC package with dict semantics.

The package may be in zip-format (a .pptx file) or expanded into a directory structure,
perhaps by unzipping a .pptx file.

### `partnames`

```python
partnames: set[PackURI] | None
```

Physical member names, or `None` for an expanded directory package.

### `rels_xml_for`

```python
rels_xml_for(partname: PackURI) -> bytes | None
```

Return optional rels item XML for `partname`.

Returns `None` if no rels item is present for `partname`. `partname` is a `PackURI`
instance.

## `PackageWriter`

```python
PackageWriter(pkg_file: str | IO[bytes], pkg_rels: _Relationships, parts: Sequence[Part])
```

Writes a zip-format OPC package to `pkg_file`.

`pkg_file` can be either a path to a zip file (a string) or a file-like object. `pkg_rels` is
the `_Relationships` object containing relationships for the package. `parts` is a sequence of
`Part` subtype instance to be written to the package.

Its single API classmethod is `write`. This class is not intended to be instantiated.

### `write`

```python
write(pkg_file: str | IO[bytes], pkg_rels: _Relationships, parts: Sequence[Part]) -> None
```

Write a physical package (.pptx file) to `pkg_file`.

The serialized package contains `pkg_rels` and `parts`, a content-types stream based on
the content type of each part, and a .rels file for each part that has relationships.

## `preflight_zip`

```python
preflight_zip(source: object) -> None
```

Validate central-directory metadata before ``ZipFile`` parses it.

An archive must declare one unambiguous central-directory region: a single
end-of-central-directory record, no multi-disk structure, and member counts,
offsets, and sizes that agree with each other and with the records actually
present. This preflight reads only fixed-size records and skips variable
fields without retaining them.
