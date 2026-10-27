<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.package`

Objects that implement reading and writing OPC packages.

## `OpcPackage`

Main API class for `python-opc`.

A new instance is constructed by calling the `open` class method with a path
to a package file or file-like object containing one.

### `after_unmarshal`

```python
after_unmarshal()
```

Entry point for any post-unmarshaling processing.

May be overridden by subclasses without forwarding call to super.

### `core_properties`

```python
core_properties: CoreProperties
```

`CoreProperties` object providing read/write access to the Dublin Core
properties for this document.

### `iter_parts`

```python
iter_parts() -> Iterator[Part]
```

Generate exactly one reference to each of the parts in the package by
performing a depth-first traversal of the rels graph.

### `iter_rels`

```python
iter_rels() -> Iterator[_Relationship]
```

Generate exactly one reference to each relationship in the package by
performing a depth-first traversal of the rels graph.

### `load_rel`

```python
load_rel(reltype: str, target: Part | str, rId: str, is_external: bool = False)
```

Return newly added `_Relationship` instance of `reltype` between this part
and `target` with key `rId`.

Target mode is set to ``RTM.EXTERNAL`` if `is_external` is `True`. Intended for
use during load from a serialized package, where the rId is well known. Other
methods exist for adding a new relationship to the package during processing.

### `main_document_part`

```python
main_document_part
```

Return a reference to the main document part for this package.

Examples include a document part for a WordprocessingML package, a presentation
part for a PresentationML package, or a workbook part for a SpreadsheetML
package.

### `next_partname`

```python
next_partname(template: str) -> PackURI
```

Return a `PackURI` instance representing partname matching `template`.

The returned part-name has the next available numeric suffix to distinguish it
from other parts of its type. `template` is a printf (%)-style template string
containing a single replacement item, a '%d' to be used to insert the integer
portion of the partname. Example: "/word/header%d.xml"

### `open`

```python
open(pkg_file: str | IO[bytes]) -> Self
```

Return an `OpcPackage` instance loaded with the contents of `pkg_file`.

### `part_related_by`

```python
part_related_by(reltype: str) -> Part
```

Return part to which this package has a relationship of `reltype`.

Raises `KeyError` if no such relationship is found and `ValueError` if more than
one such relationship is found.

### `parts`

```python
parts: list[Part]
```

Return a list containing a reference to each of the parts in this package.

### `relate_to`

```python
relate_to(part: Part, reltype: str)
```

Return rId key of new or existing relationship to `part`.

If a relationship of `reltype` to `part` already exists, its rId is returned. Otherwise a
new relationship is created and that rId is returned.

### `rels`

```python
rels()
```

Return a reference to the `Relationships` instance holding the collection of
relationships for this package.

### `save`

```python
save(pkg_file: str | IO[bytes])
```

Save this package to `pkg_file`, a path or a stream positioned at byte zero.

Serializes to a sibling temp file, reparses it to confirm it opens, then replaces the
destination. A failed save leaves the original untouched and never leaves behind a
package Word cannot open. An existing destination keeps its permission bits. A symlink
destination is followed and its target replaced.

Raises `MalformedPackageError` when the serialized output will not reparse, and
`OSError` for a stream in append mode, a stream positioned past byte zero, or a
destination symlink that moves mid-save.

## `Unmarshaller`

Hosts static methods for unmarshalling a package from a `PackageReader`.

### `unmarshal`

```python
unmarshal(pkg_reader, package, part_factory)
```

Construct graph of parts and realized relationships based on the contents of
`pkg_reader`, delegating construction of each part to `part_factory`.

Package relationships are added to `pkg`.
