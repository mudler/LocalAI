<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.package`

WordprocessingML Package class and related objects.

## `CompareFinding`

```python
CompareFinding(kind: str, story: str, detail: str) -> None
```

One difference compare REPORTS instead of redlining (declared limit).

### `detail`

```python
detail: str
```

### `kind`

```python
kind: str
```

### `story`

```python
story: str
```

### `to_dict`

```python
to_dict() -> dict
```

## `CompareResult`

```python
CompareResult(document: 'Document', findings: List[CompareFinding], revision_count: int, stories: Tuple[str, ...]) -> None
```

The redlined document plus everything compare could not redline.

### `document`

```python
document: 'Document'
```

### `findings`

```python
findings: List[CompareFinding]
```

### `revision_count`

```python
revision_count: int
```

### `stories`

```python
stories: Tuple[str, ...]
```

### `to_dict`

```python
to_dict() -> dict
```

## `ImageParts`

```python
ImageParts()
```

Collection of `ImagePart` objects corresponding to images in the package.

### `append`

```python
append(item: ImagePart)
```

### `get_or_add_image_part`

```python
get_or_add_image_part(image_descriptor: str | IO[bytes]) -> ImagePart
```

Return `ImagePart` object containing image identified by `image_descriptor`.

The image-part is newly created if a matching one is not present in the
collection.

## `Package`

Bases: `OpcPackage`

Customizations specific to a WordprocessingML package.

### `after_unmarshal`

```python
after_unmarshal()
```

Called by loading code after all parts and relationships have been loaded.

This method affords the opportunity for any required post-processing.

### `get_or_add_image_part`

```python
get_or_add_image_part(image_descriptor: str | IO[bytes]) -> ImagePart
```

Return `ImagePart` containing image specified by `image_descriptor`.

The image-part is newly created if a matching one is not already present in the
collection.

### `image_parts`

```python
image_parts() -> ImageParts
```

`ImageParts` collection object for this package.

## `PackageDiagnosis`

```python
PackageDiagnosis(path: str, readable: bool, kind: str, problems: Tuple[str, ...]) -> None
```

Typed triage for a file `docx.Document()` may refuse or crash on.

`readable` means "this package can open it as a WordprocessingML
document"; `kind` names what the file actually is; `problems` say why it
is not readable (empty when it is).

### `kind`

```python
kind: str
```

### `path`

```python
path: str
```

### `problems`

```python
problems: Tuple[str, ...]
```

### `readable`

```python
readable: bool
```

### `to_dict`

```python
to_dict() -> dict
```

## `PackageDiff`

```python
PackageDiff(added: Tuple[str, ...], removed: Tuple[str, ...], changed: Tuple[PartDiff, ...], byte_identical_count: int) -> None
```

Part-by-part comparison of two OPC packages.

### `added`

```python
added: Tuple[str, ...]
```

### `byte_identical_count`

```python
byte_identical_count: int
```

### `changed`

```python
changed: Tuple[PartDiff, ...]
```

### `is_semantically_empty`

```python
is_semantically_empty: bool
```

True when the packages hold the same parts with the same meaning.

### `removed`

```python
removed: Tuple[str, ...]
```

### `semantic_changed_parts`

```python
semantic_changed_parts() -> Tuple[str, ...]
```

### `to_dict`

```python
to_dict() -> dict
```

## `PartDiff`

```python
PartDiff(part: str, kind: str, before_sha256: str, after_sha256: str, semantic_change: bool) -> None
```

One byte-changed part in a package comparison.

### `after_sha256`

```python
after_sha256: str
```

### `before_sha256`

```python
before_sha256: str
```

### `kind`

```python
kind: str
```

### `part`

```python
part: str
```

### `semantic_change`

```python
semantic_change: bool
```

### `to_dict`

```python
to_dict() -> dict
```

## `PatchSaveResult`

```python
PatchSaveResult(restored_parts: Tuple[str, ...], changed_parts: Tuple[str, ...], added_parts: Tuple[str, ...], removed_parts: Tuple[str, ...], verbatim_copy: bool) -> None
```

Outcome of a `patch_save` call.

### `added_parts`

```python
added_parts: Tuple[str, ...]
```

### `changed_parts`

```python
changed_parts: Tuple[str, ...]
```

### `removed_parts`

```python
removed_parts: Tuple[str, ...]
```

### `restored_parts`

```python
restored_parts: Tuple[str, ...]
```

### `to_dict`

```python
to_dict() -> dict
```

### `verbatim_copy`

```python
verbatim_copy: bool
```

## `StoryTextDiff`

```python
StoryTextDiff(story: str, diff_lines: Tuple[str, ...]) -> None
```

### `diff_lines`

```python
diff_lines: Tuple[str, ...]
```

### `story`

```python
story: str
```

### `to_dict`

```python
to_dict() -> dict
```

## `TextDiff`

```python
TextDiff(stories: Tuple[StoryTextDiff, ...]) -> None
```

Per-story unified diffs of visible block text.

### `changed_line_count`

```python
changed_line_count: int
```

### `is_empty`

```python
is_empty: bool
```

### `stories`

```python
stories: Tuple[StoryTextDiff, ...]
```

### `to_dict`

```python
to_dict() -> dict
```

## `compare`

```python
compare(original, revised, *, author: str, date: 'Optional[dt.datetime]' = None, granularity: str = 'word', materialize: Optional[str] = None) -> CompareResult
```

A `CompareResult`: `original` plus tracked changes producing `revised`.

Takes paths or bytes. `granularity` is "word" or "block". Inputs carrying pending
revisions refuse unless `materialize` ("accept" | "reject") resolves them on in-memory
copies, leaving the files on disk untouched. Refuses any edit it cannot express as a
clean redline.

## `diagnose`

```python
diagnose(path: _PathLike) -> PackageDiagnosis
```

Why the file at `path` can or cannot be opened, as a `PackageDiagnosis`.

Returns instead of raising, so you can triage untrusted input before opening it.
`Document()` refuses corrupt and encrypted packages with `MalformedPackageError` but
still raises a bare `ValueError` for macro-enabled and template files.

## `diff_package`

```python
diff_package(path_a: _PathLike, path_b: _PathLike) -> PackageDiff
```

Part-by-part diff of the OPC packages at `path_a` and `path_b`.

XML parts (`*.xml`, `*.rels`) compare semantically, binary parts by bytes. A
byte-changed XML part that fails to parse counts as semantically changed, never
silently equal. Raises `MalformedPackageError` when either package will not open.

## `patch_save`

```python
patch_save(original_path: _PathLike, document: 'Document', out_path: _PathLike) -> PatchSaveResult
```

Save `document` to `out_path`, restoring original bytes wherever possible.

The document is serialized normally, then every XML part semantically identical to its
counterpart in `original_path` gets that counterpart's exact original bytes back, a
narrow save that keeps unrelated parts byte-stable through the open/edit/save cycle.
When nothing changed, `out_path` becomes a verbatim copy of the original.

`original_path == out_path` is permitted; the original bytes are read up front and the
write is atomic (temp file + rename), so the original survives a mid-write failure. An
existing destination keeps its permission bits; a new one inherits `original_path`'s.
Creates the destination's parent directory when it is missing.

Returns a `PatchSaveResult` naming what it restored and what it rewrote. Raises
`MalformedPackageError` when `original_path`, the serialized document, or the finished
output fails to reparse, and `OSError` when a destination symlink moves mid-save.

## `pending_changes`

```python
pending_changes(path: _PathLike) -> TextDiff
```

What the document's pending revisions would change if all were accepted.

Diffs the "original" view against the "current" view, so an empty `TextDiff` means no
visible TEXT change rather than a clean document: formatting-only, table-structure and
same-place move revisions produce no diff lines. Ask `Document.revisions` whether markup
remains.

## `text_diff`

```python
text_diff(path_a: _PathLike, path_b: _PathLike, *, view: str = 'current') -> TextDiff
```

What changed, textually, between the documents at `path_a` and `path_b`.

## `xml_equivalent`

```python
xml_equivalent(a: bytes, b: bytes) -> bool
```

True when `a` and `b` are structurally identical XML documents.

Tags and attribute names compare in Clark notation, so namespace *prefix*
choices never matter while namespace URIs always do. Attribute order is
insignificant (XML defines attributes as unordered); child order is
significant; text and tail content compare verbatim, whitespace included
(a canonicalizer that trims a meaningful trailing space would corrupt
documents through `patch_save`). Prolog/epilog comments and
processing instructions are compared too.

Raises `lxml.etree.XMLSyntaxError` on malformed input and
``UnsupportedXmlError`` on DTD-bearing input — this function never guesses.

Known limit: attribute VALUES holding
QNames compare textually, so a prefix rebound to a different URI while
the QName text stays identical is not detected. OOXML producers keep the
standard prefixes, and the error direction in `diff_package` remains
conservative for everything else.
