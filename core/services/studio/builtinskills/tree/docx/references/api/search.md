<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.search`

Exact and normalized text search over visibility-complete traversal.

## `ReplaceAllResult`

```python
ReplaceAllResult(replaced_count: int, results: Tuple[ReplaceResult, ...], refused: Tuple[dict, ...]) -> None
```

Outcome of a `replace_all` call: per-match results, never silent.

### `refused`

```python
refused: Tuple[dict, ...]
```

### `replaced_count`

```python
replaced_count: int
```

### `results`

```python
results: Tuple[ReplaceResult, ...]
```

### `to_dict`

```python
to_dict() -> dict
```

## `ReplaceResult`

```python
ReplaceResult(story: str, deleted_text: str, inserted_text: str, tracked: bool, revision_ids: Tuple[int, ...], preserved_revision_ids: Tuple[int, ...] = (), preserved_formatting_regions: bool = False) -> None
```

Outcome of a `Span.replace` call.

`deleted_text` and `inserted_text` cover the whole span for an untracked replace but only
the affix-trimmed middle for a tracked one, so comparing them against `Span.text` is
wrong for tracked edits. `preserved_formatting_regions` reports whether an ordinary
replacement kept every changed formatting region rather than intentionally inheriting the
changed interval's starting run properties; for a no-op, it records that no formatting
changed. It is independent of revision evidence.

### `deleted_text`

```python
deleted_text: str
```

### `inserted_text`

```python
inserted_text: str
```

### `preserved_formatting_regions`

```python
preserved_formatting_regions: bool = False
```

### `preserved_revision_ids`

```python
preserved_revision_ids: Tuple[int, ...] = ()
```

### `revision_ids`

```python
revision_ids: Tuple[int, ...]
```

### `story`

```python
story: str
```

### `to_dict`

```python
to_dict() -> dict
```

### `tracked`

```python
tracked: bool
```

## `Span`

```python
Span(text: str, story: str, anchor: Anchor, in_insert: bool, in_delete: bool, in_content_control: bool, in_text_box: bool, in_field: bool, crosses_paragraphs: bool, _document: 'Document', _atoms: 'List[_Atom]', _start_offset: int, _end_offset: int, _raw_start: int, _match_start: int, _consumed: bool = False) -> None
```

A visible-text interval mapped back to its concrete text atoms.

Spans hold live references into the document tree and go stale when the
underlying text changes; every operation revalidates first and raises
`TargetNotFoundError` on staleness. ``match_policy`` records the policy
that selected a search-produced span, or is ``None`` for a span constructed
from already-known live offsets.

### `anchor`

```python
anchor: Anchor
```

### `comment`

```python
comment(text: str, *, author: str, initials: Optional[str] = None, date: Optional[dt.datetime] = None) -> 'object'
```

Anchor a new comment to exactly this span's text, and return the upstream `Comment`.

Reach for this over `Document.add_comment` when the anchor must match exact text rather
than whole runs. Splits boundary runs, and creates `/word/comments.xml` on first use.
Only the main document story carries comments. Refuses a protected document, a stale
span, and a locked or data-bound control surface.

### `crosses_paragraphs`

```python
crosses_paragraphs: bool
```

### `in_content_control`

```python
in_content_control: bool
```

### `in_delete`

```python
in_delete: bool
```

### `in_field`

```python
in_field: bool
```

### `in_insert`

```python
in_insert: bool
```

### `in_text_box`

```python
in_text_box: bool
```

### `match_policy`

```python
match_policy: 'Optional[str]' = field(default=None, init=False)
```

### `replace`

```python
replace(new_text: str, *, tracked: bool = False, author: Optional[str] = None, date: Optional[dt.datetime] = None, preserve_revision: bool = False) -> ReplaceResult
```

Replace this span's text and return machine-readable change evidence.

The default is an untracked edit over one proved structural-owner region.
When maximal exact prefix/suffix alignment identifies one changed interval, unchanged
affix text stays in its existing atoms. A nonempty replacement inherits the complete
direct run properties of the changed interval's starting text run; consumed later runs
may therefore collapse intentionally. Distinct inline wrapper owners, semantic-scope
crossings, positional-marker crossings, and ambiguous affix or pure-insertion boundaries
refuse. `tracked=True` uses the same unique localization and formatting inheritance;
tracked deletion pieces retain each source run's properties.
A successful tracked change consumes the span; a direct tracked no-op is refused.

`preserve_revision=True` explicitly permits a current-view span wholly owned by one
existing `w:ins` to be corrected without changing that insertion's id, author, date,
or accept/reject meaning; outside revision markup it behaves like an ordinary untracked
edit. The corrected text remains attributed to the existing insertion's author and date.
`preserved_formatting_regions` and `preserved_revision_ids` report independent
guarantees; `revision_ids` remains reserved for newly authored tracked revisions. Every
successful text-changing replacement consumes
the supplied span; use the returned result and re-find the text before another operation.
A no-op, refusal, or rolled-back mutation leaves the span reusable. Refuses a protected
document, a stale or foreign span, and unsafe field,
control, revision, bookmark, whitespace, or paragraph-boundary structures before
mutation.

### `story`

```python
story: str
```

### `text`

```python
text: str
```

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `find_one`

```python
find_one(document: 'Document', needle: str, *, nth: Optional[int] = None, story: Optional[str] = None, view: str = 'current', match: str = 'exact') -> Span
```

The single span matching `needle`, or a typed refusal.

Exact matching is the default; pass ``match="normalized"`` to opt into
folded targeting. Zero matches raise `TargetNotFoundError`. Two or more
raise `AmbiguousTargetError`. `nth` and `story` explicitly narrow the set.

## `find_text`

```python
find_text(document: 'Document', needle: str, *, nth: Optional[int] = None, near: Optional[str] = None, story: Optional[str] = None, view: str = 'current', match: str = 'exact') -> 'List[Span]'
```

Every span of `needle` in `document` under the selected match policy.

`story` limits the search to one story part (e.g. "word/document.xml");
`near` ranks every match by distance to the nearest occurrence of `near`'s
text under the same policy in the same story. Ranking remains complete
when context is missing or tied. `nth` (1-based) instead selects a match
by document position and cannot be combined with `near`. Exact matching is
the default. Normalized matching folds case, typography, and whitespace.
Both assemble across fragmented runs; exact matching represents each
paragraph boundary as one literal ``\n``.

## `normalize_text`

```python
normalize_text(value: str) -> str
```

`value` normalized for matching: folded punctuation, collapsed
whitespace, casefolded. Never applied to document content on write.

ANY Unicode whitespace collapses to a single ASCII space (`\s+`, not just
the spaces in the table) so needles and document text normalize
identically no matter which exotic space either side carries.

## `replace_all`

```python
replace_all(document: 'Document', needle: str, new_text: str, *, story: Optional[str] = None, view: str = 'current', match: str = 'exact', tracked: bool = False, author: Optional[str] = None, date: Optional[dt.datetime] = None, preserve_revision: bool = False) -> ReplaceAllResult
```

Replace every match of `needle` in one pass, and return a `ReplaceAllResult`.

``match`` defaults to literal exact matching; ``"normalized"`` explicitly
enables folded matching. One scan finds all matches, then replacements
apply in reverse raw document order within each story, so no pending match
shifts. A refusal on one match is recorded in `refused` and the rest
proceed; a stale span aborts the batch instead, rolling back every
replacement already applied. Matches already equal to `new_text` are
skipped. `preserve_revision` has the same contract as `Span` ``.replace``
and is forwarded to every match without adding a transaction per match.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.
