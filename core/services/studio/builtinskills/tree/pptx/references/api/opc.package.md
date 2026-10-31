<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.opc.package`

Fundamental Open Packaging Convention (OPC) objects.

## `OpcPackage`

```python
OpcPackage(pkg_file: str | IO[bytes])
```

Bases: `_RelatableMixin`

Main API class for `python-opc`.

A new instance is constructed by calling the `open` classmethod with a path to a package
file or file-like object containing a package (.pptx file).

### `drop_rel`

```python
drop_rel(rId: str) -> None
```

Remove relationship identified by `rId`.

### `iter_parts`

```python
iter_parts() -> Iterator[Part]
```

Generate exactly one reference to each part in the package.

### `iter_rels`

```python
iter_rels() -> Iterator[_Relationship]
```

Generate exactly one reference to each relationship in package.

Performs a depth-first traversal of the rels graph.

### `main_document_part`

```python
main_document_part: PresentationPart
```

Return `Part` subtype serving as the main document part for this package.

In this case it will be a `Presentation` part.

### `next_partname`

```python
next_partname(tmpl: str) -> PackURI
```

Return `PackURI` next available partname matching `tmpl`.

`tmpl` is a printf (%)-style template string containing a single replacement item, a '%d'
to be used to insert the integer portion of the partname. Example:
'/ppt/slides/slide%d.xml'

### `open`

```python
open(pkg_file: str | IO[bytes]) -> Self
```

Return an `OpcPackage` instance loaded with the contents of `pkg_file`.

### `save`

```python
save(pkg_file: str | IO[bytes]) -> None
```

Save this package to `pkg_file`.

`pkg_file` can be a filesystem path (`str` or `os.PathLike`) or a file-like object open
for writing bytes.

A path destination is written atomically: the package is serialized into a temporary
file in the destination's directory and moved into place with `os.replace()`, so a
failure part-way through leaves any existing file untouched. Symlinks are resolved, so
the file a link names is the file that is written.

A stream destination is serialized into a private staging buffer and then written
straight through, so no bytes reach the stream unless the whole package serialized. Only
`write` is required of the stream. The package is written at the stream's current
position; past that, bytes beyond the package are the caller's and are left alone.

What a failure *during* that copy costs depends on what the destination supports. One
that can be read, rewound and truncated has its prior contents and cursor restored, and
if that restore itself fails the original error is replaced by a `RuntimeError` carrying
it as `__cause__`. A write-only or unseekable sink keeps whatever partial package landed;
a caller writing to a pipe cannot expect otherwise. The same capability decides
truncation: a readable stream positioned at the start is truncated to the package, so no
tail of a previous document survives, while a write-only stream keeps that tail.

Atomic path writes are a deliberate departure from the v0 rule that `save()` behavior is
unchanged from upstream, accepted because upstream serialized directly into the
destination, so a mid-write failure on the ordinary `prs.save(same_path)` pattern
destroyed the deck being edited, irrecoverably.

The costs of replacing rather than overwriting: owner, group, ACLs, extended attributes
and hard links do not survive (mode bits are carried over), and the destination's
*directory* must be writable, not just the file.

## `Part`

```python
Part(partname: PackURI, content_type: str, package: Package, blob: bytes | None = None)
```

Bases: `_RelatableMixin`

Base class for package parts.

Provides common properties and methods, but intended to be subclassed in client code to
implement specific part behaviors. Also serves as the default class for parts that are not yet
given specific behaviors.

### `blob`

```python
blob: bytes
```

Contents of this package part as a sequence of bytes.

Intended to be overridden by subclasses. Default behavior is to return the blob initial
loaded during `Package.open()` operation.

### `content_type`

```python
content_type() -> str
```

Content-type (MIME-type) of this part.

### `load`

```python
load(partname: PackURI, content_type: str, package: Package, blob: bytes) -> Self
```

Return `cls` instance loaded from arguments.

This one is a straight pass-through, but subtypes may do some pre-processing, see XmlPart
for an example.

### `load_rels_from_xml`

```python
load_rels_from_xml(xml_rels: CT_Relationships, parts: dict[PackURI, Part]) -> None
```

load _Relationships for this part from `xml_rels`.

Part references are resolved using the `parts` dict that maps each partname to the loaded
part with that partname. These relationships are loaded from a serialized package and so
already have assigned rIds. This method is only used during package loading.

### `package`

```python
package() -> Package
```

Package this part belongs to.

### `partname`

```python
partname: PackURI
```

`PackURI` partname for this part, e.g. "/ppt/slides/slide1.xml".

### `rels`

```python
rels() -> _Relationships
```

Collection of relationships from this part to other parts.

## `PartFactory`

Constructs a registered subtype of `Part`.

Client code can register a subclass of `Part` to be used for a package blob based on its
content type.

### `part_type_for`

```python
part_type_for: dict[str, type[Part]] = {}
```

## `XmlPart`

```python
XmlPart(partname: PackURI, content_type: str, package: Package, element: BaseOxmlElement)
```

Bases: `Part`

Base class for package parts containing an XML payload, which is most of them.

Provides additional methods to the `Part` base class that take care of parsing and
reserializing the XML payload and managing relationships to other parts.

### `blob`

```python
blob: bytes
```

bytes XML serialization of this part.

### `drop_rel`

```python
drop_rel(rId: str) -> None
```

Remove relationship identified by `rId` if its reference count is under 2.

Relationships with a reference count of 0 are implicit relationships. Note that only XML
parts can drop relationships.

### `load`

```python
load(partname: PackURI, content_type: str, package: Package, blob: bytes)
```

Return instance of `cls` loaded with parsed XML from `blob`.

### `part`

```python
part
```

This part.

This is part of the parent protocol, "children" of the document will not know the part
that contains them so must ask their parent object. That chain of delegation ends here for
child objects.
