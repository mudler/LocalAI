<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.story`

Visibility-complete story traversal and inspection (paper-docx, opt-in).

## `Anchor`

```python
Anchor(story: str, index: int, content_hash: str) -> None
```

Legacy, inert location evidence: story part + index + content hash.

``Anchor`` remains serializable for historical search and revision result
data. It is not a mutation-capable block target; reacquire a live `Block`
or `Span` instead.

### `content_hash`

```python
content_hash: str
```

### `index`

```python
index: int
```

### `story`

```python
story: str
```

### `to_dict`

```python
to_dict() -> 'Dict[str, object]'
```

## `BLIND_REGION_KEYS`

```python
BLIND_REGION_KEYS = ('tracked_insertions', 'tracked_deletions', 'moves', 'format_changes', 'content_controls', 'text_boxes', 'fields', 'math', 'embedded_objects', 'alt_chunks', 'hidden_text')
```

## `Block`

```python
Block(story: str, kind: str, index: int, anchor: Anchor, text: str, style_id: Optional[str], in_insert: bool, in_delete: bool, in_content_control: bool, in_text_box: bool, has_field: bool, table: Optional[TableShape], _document: 'Optional[Document]' = None, _element: 'Optional[_Element]' = None, _story_root: 'Optional[_Element]' = None, _parent: 'Optional[_Element]' = None, _container_elements: 'Tuple[_Element, ...]' = (), _view: str = 'current') -> None
```

One live, owner-bound paragraph or table observed during traversal.

### `anchor`

```python
anchor: Anchor
```

### `has_field`

```python
has_field: bool
```

### `in_content_control`

```python
in_content_control: bool
```

### `in_delete`

```python
in_delete: bool
```

### `in_insert`

```python
in_insert: bool
```

### `in_text_box`

```python
in_text_box: bool
```

### `index`

```python
index: int
```

### `kind`

```python
kind: str
```

### `story`

```python
story: str
```

### `style_id`

```python
style_id: Optional[str]
```

### `table`

```python
table: Optional[TableShape]
```

### `text`

```python
text: str
```

### `to_dict`

```python
to_dict() -> 'Dict[str, object]'
```

## `Outline`

```python
Outline(story_parts: Tuple[str, ...], blocks: Tuple[Block, ...], blind_region_counts: Dict[str, int]) -> None
```

Inspection snapshot of a document: every block in every story part.

### `blind_region_counts`

```python
blind_region_counts: Dict[str, int]
```

### `blocks`

```python
blocks: Tuple[Block, ...]
```

### `story_parts`

```python
story_parts: Tuple[str, ...]
```

### `to_dict`

```python
to_dict() -> 'Dict[str, object]'
```

## `TableShape`

```python
TableShape(rows: int, columns: int, has_merges: bool, has_nested_table: bool) -> None
```

### `columns`

```python
columns: int
```

### `has_merges`

```python
has_merges: bool
```

### `has_nested_table`

```python
has_nested_table: bool
```

### `rows`

```python
rows: int
```

### `to_dict`

```python
to_dict() -> 'Dict[str, object]'
```

## `VIEWS`

```python
VIEWS = ('current', 'original', 'all')
```

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `content_hash`

```python
content_hash(text: str) -> str
```

First 8 hex chars of SHA-256 over the block's normalized text.

## `iter_blocks`

```python
iter_blocks(document: 'Document', *, view: str = 'current') -> Iterator[Block]
```

Every block in every story part of `document`, in document order.

`view` selects the text layer: "current" (insertions in, deletions out —
the document if all changes were accepted), "original" (deletions in,
insertions out), or "all" (everything).

## `normalize_text`

```python
normalize_text(value: str) -> str
```

`value` normalized for matching: folded punctuation, collapsed
whitespace, casefolded. Never applied to document content on write.

ANY Unicode whitespace collapses to a single ASCII space (`\s+`, not just
the spaces in the table) so needles and document text normalize
identically no matter which exotic space either side carries.

## `outline`

```python
outline(document: 'Document', *, view: str = 'current') -> Outline
```

Inspection snapshot: story parts, all blocks, blind-region counts.

Deterministic: the same document yields byte-identical `to_dict()` output
on every call (inspection determinism).

## `story_parts`

```python
story_parts(document: 'Document') -> Tuple[str, ...]
```

Every story part present in `document`, in traversal order.
