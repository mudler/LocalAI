<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.package`

Overall .pptx package.

## `Package`

Bases: `OpcPackage`

An overall .pptx package.

### `core_properties`

```python
core_properties() -> CorePropertiesPart
```

Instance of `CoreProperties` holding read/write Dublin Core doc properties.

Creates a default core properties part if one is not present (not common).

### `get_or_add_image_part`

```python
get_or_add_image_part(image_file: str | IO[bytes])
```

Return an `ImagePart` object containing the image in *image_file*. If
the image part already exists in this package, it is reused,
otherwise a new one is created.

### `get_or_add_media_part`

```python
get_or_add_media_part(media)
```

Return a `MediaPart` object containing the media in *media*.

If a media part for this media bytestream ("file") is already present
in this package, it is reused, otherwise a new one is created.

### `next_image_partname`

```python
next_image_partname(ext: str) -> PackURI
```

Return a `PackURI` instance representing the next available image partname.

Partname uses the next available sequence number. *ext* is used as the extention on the
returned partname.

### `next_media_partname`

```python
next_media_partname(ext)
```

Return `PackURI` instance for next available media partname.

Partname is first available, starting at sequence number 1. Empty
sequence numbers are reused. *ext* is used as the extension on the
returned partname.

### `presentation_part`

```python
presentation_part
```

Reference to the `Presentation` instance contained in this package.

## `PackageDiff`

```python
PackageDiff(deltas: Tuple[PartDelta, ...]) -> None
```

Part-by-part semantic diff between two packages. Schema "paper-package-diff" v1.

``deltas`` holds one `PartDelta` per package member that was added, removed,
or semantically changed; it is empty when the two packages are equivalent.

### `deltas`

```python
deltas: Tuple[PartDelta, ...]
```

### `is_empty`

```python
is_empty: bool
```

True when no part changed. Check it to recognize a no-op save.

### `to_dict`

```python
to_dict() -> dict
```

Return the package diff as a JSON-ready dict under the `paper-package-diff` schema.

## `PartDelta`

```python
PartDelta(partname: str, kind: str, change: str, detail: str) -> None
```

One differing package member.

### `change`

```python
change: str
```

### `detail`

```python
detail: str
```

### `kind`

```python
kind: str
```

### `partname`

```python
partname: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return this part's change as a JSON-ready dict.

## `diff_package`

```python
diff_package(path_a: str, path_b: str) -> PackageDiff
```

Return the `PackageDiff` between the packages at `path_a` and `path_b`.

XML members are compared semantically. Valid relationship parts compare complete bindings,
ignoring child order, omitted versus explicit ``Internal`` TargetMode, and accepted
absolute-versus-source-relative spellings that resolve to the same internal member.
Relationship IDs and types, whether a binding is external, external target spellings, and
unknown attributes remain significant. Valid content-type manifests compare the effective
type assigned to every package member; unmatched defaults remain significant. Unsupported
manifests, including overrides for absent members, fall back to the existing
declaration-order-insensitive XML comparison.

Other XML uses the order-sensitive `xml_equivalent`; binary members compare by bytes. Members
appearing in only one package report as "added" (only in `path_b`) or "removed" (only in
`path_a`). Deltas are sorted by partname; the returned `PackageDiff` retains schema version 1.

## `patch_save`

```python
patch_save(original_path: str, document: str, out_path: str) -> PackageDiff
```

Save `document` to `out_path`, restoring original bytes for unchanged XML parts.

Compare-based narrow save: `document` (a `Presentation`) is serialized normally, then every XML
member that is semantically identical to its counterpart in `original_path` is written with the
ORIGINAL bytes, so unrelated parts never churn. Returns the residual `PackageDiff` between
`original_path` and `out_path`.

Writes are deterministic — entry order is `[Content_Types].xml`, `_rels/.rels`, then all
remaining members sorted; every entry timestamp is fixed to 1980-01-01 — and atomic: the package
is built in a temp file in `out_path`'s directory and moved into place with `os.replace`, so a
mid-write failure leaves any existing `out_path` untouched. When nothing changed at all,
`out_path` is written as an exact byte copy of `original_path`.

Valid relationship collections compare by their complete bindings rather than serialization
order, and valid content-type manifests compare member-by-member effective assignments while
retaining unmatched defaults. Producer-specific serialization choices in those package
registries therefore do not prevent a byte-identical no-op save. Well-formed ambiguous or
unsupported structures receive no specialized normalization: relationships fall back to
ordinary XML comparison, while content-type manifests retain the existing
declaration-order-insensitive XML comparison. When comparison requires parsing, malformed XML
and prohibited DTD/entity constructs raise `ValueError`.

"Nothing changed" is decided over the members that can be parts: none added, none removed, each
semantically identical to its counterpart. A ZIP folder record such as `ppt/` is not a part, so
`save()` structurally cannot emit one and its absence from the serialized candidate never
evidences a change. Such a record therefore survives a no-op round trip — the byte copy
reproduces it — and is dropped by an actual edit, which rebuilds the package from the members
`save()` emitted.

Not interchangeable with `.Presentation.save`, which is also atomic on a path: atomicity
is how the bytes land, narrowness is which bytes get written. `save()` re-serializes every part,
so even an unchanged part gets new bytes; `patch_save` restores the original bytes for every
part that is semantically identical.

Symlinked destinations are resolved, so the file a link names is the file replaced.

Raises `UnsupportedStructureError` when `original_path` is not a readable zip package (before
anything is written) and `ValueError` when `document` cannot save itself.

## `xml_equivalent`

```python
xml_equivalent(a: Union[bytes, str], b: Union[bytes, str]) -> bool
```

Return True when `a` and `b` are semantically equivalent XML documents.

Comparison is Canonical XML (C14N 2.0) with prefixes rewritten, so differences in
attribute order, namespace-prefix spelling, XML declaration, self-closing-tag style, and
pretty-print indentation are equivalent — while element order, attribute values, and all
potentially meaningful text compare exactly.

Whitespace handling is deliberately asymmetric: a whitespace-only text
node is ignored ONLY where its parent element has element children (structural
indentation; OOXML defines no mixed content, so such whitespace can never render). Text
of element-childless elements — `a:t` and friends — is never normalized in any way: two
documents differing only by a trailing space inside a text node are NOT equivalent.

Raises `ValueError` when either argument is not well-formed XML or contains a DTD or
entity declaration.
