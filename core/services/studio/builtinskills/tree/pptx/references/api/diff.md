<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.diff`

Deck diff - the verification mirror (paper-pptx addition).

## `BulletShift`

```python
BulletShift(part: str, shape: ShapeRef, before_location: dict, after_location: dict, text: str, before: dict, after: dict) -> None
```

One paragraph whose resolved bullet changed between the two decks.

Bullets live only in formatting, so a list losing its bullets changes no text and no
field marker: `text_changes` stays empty while the slide visibly loses every glyph.
This is the facet that reports it.

Paragraphs qualify only when their exact text-and-field value is unique on both sides
and lies in the canonical unchanged prefix or suffix. Repeated or changed-region
paragraphs are skipped rather than paired by position.

Fields:

* ``part`` -- partname of the slide the paragraph lives on.
* ``shape`` -- stable shape identity plus current display name.
* ``before_location`` / ``after_location`` -- container-local paragraph locations.
* ``text`` -- the unchanged paragraph's literal text.
* ``before`` / ``after`` -- the resolved ``bullet``, ``bullet_font`` and ``bullet_size``
  payloads from `pptx.inspect.effective_paragraph_format` on each side.

### `after`

```python
after: dict
```

### `after_location`

```python
after_location: dict
```

### `before`

```python
before: dict
```

### `before_location`

```python
before_location: dict
```

### `part`

```python
part: str
```

### `shape`

```python
shape: ShapeRef
```

### `text`

```python
text: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return this bullet change as a JSON-ready dict, before and after values included.

## `DeckDiff`

```python
DeckDiff(detail: str, slides_added: Tuple[SlideRef, ...] = (), slides_removed: Tuple[SlideRef, ...] = (), slides_moved: Tuple[MovedSlide, ...] = (), slide_changes: Tuple[SlideChange, ...] = tuple(), package_changes: tuple = tuple()) -> None
```

The whole comparison. `.to_dict()` is deterministic and goldenable.

### `detail`

```python
detail: str
```

### `is_empty`

```python
is_empty: bool
```

True when the two decks match. Check it to skip reporting a no-op edit.

### `package_changes`

```python
package_changes: tuple = field(default_factory=tuple)
```

### `slide_changes`

```python
slide_changes: Tuple[SlideChange, ...] = field(default_factory=tuple)
```

### `slides_added`

```python
slides_added: Tuple[SlideRef, ...] = ()
```

### `slides_moved`

```python
slides_moved: Tuple[MovedSlide, ...] = ()
```

### `slides_removed`

```python
slides_removed: Tuple[SlideRef, ...] = ()
```

### `to_dict`

```python
to_dict() -> dict
```

Return the whole diff as a JSON-ready dict stamped with its schema and version.

## `EffectiveShift`

```python
EffectiveShift(part: str, shape: ShapeRef, before_location: dict, after_location: dict, before_run_index: int, after_run_index: int, text: str, before: dict, after: dict) -> None
```

One uniquely matched run in a unique unchanged paragraph whose resolved font changed.

### `after`

```python
after: dict
```

### `after_location`

```python
after_location: dict
```

### `after_run_index`

```python
after_run_index: int
```

### `before`

```python
before: dict
```

### `before_location`

```python
before_location: dict
```

### `before_run_index`

```python
before_run_index: int
```

### `part`

```python
part: str
```

### `shape`

```python
shape: ShapeRef
```

### `text`

```python
text: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return this location-aware shift as a JSON-ready dictionary.

## `MovedSlide`

```python
MovedSlide(slide_id: int, from_position: int, to_position: int) -> None
```

A slide that kept its identity and changed position.

### `from_position`

```python
from_position: int
```

### `slide_id`

```python
slide_id: int
```

### `to_dict`

```python
to_dict() -> dict
```

Return this move as a JSON-ready dict.

### `to_position`

```python
to_position: int
```

## `SCHEMA_NAME`

```python
SCHEMA_NAME = 'paper-deck-diff'
```

## `SCHEMA_VERSION`

```python
SCHEMA_VERSION = 5
```

## `ShapeRef`

```python
ShapeRef(shape_id: int, name: str) -> None
```

Identifies a slide-scoped shape by stable id and display name.

### `name`

```python
name: str
```

### `shape_id`

```python
shape_id: int
```

### `to_dict`

```python
to_dict() -> dict
```

Return this shape reference as a JSON-ready dict.

## `SlideChange`

```python
SlideChange(slide_id: int, shapes_added: Tuple[ShapeRef, ...] = (), shapes_removed: Tuple[ShapeRef, ...] = (), geometry_changes: Tuple[dict, ...] = (), images_replaced: Tuple[ShapeRef, ...] = (), chart_data_changes: Tuple[dict, ...] = (), table_structure_changes: Tuple[dict, ...] = (), text_changes: Tuple[dict, ...] = (), notes_change: Optional[dict] = None, effective_shifts: tuple = (), bullet_shifts: tuple = ()) -> None
```

Within-slide deltas for one id-matched slide pair. Empty facets are omitted
from the payload, so an all-empty change never appears in `slide_changes`.

### `bullet_shifts`

```python
bullet_shifts: tuple = ()
```

### `chart_data_changes`

```python
chart_data_changes: Tuple[dict, ...] = ()
```

### `effective_shifts`

```python
effective_shifts: tuple = ()
```

### `geometry_changes`

```python
geometry_changes: Tuple[dict, ...] = ()
```

### `images_replaced`

```python
images_replaced: Tuple[ShapeRef, ...] = ()
```

### `is_empty`

```python
is_empty: bool
```

True when nothing on this slide changed. Check it before adding the slide to a report.

### `notes_change`

```python
notes_change: Optional[dict] = None
```

### `shapes_added`

```python
shapes_added: Tuple[ShapeRef, ...] = ()
```

### `shapes_removed`

```python
shapes_removed: Tuple[ShapeRef, ...] = ()
```

### `slide_id`

```python
slide_id: int
```

### `table_structure_changes`

```python
table_structure_changes: Tuple[dict, ...] = ()
```

### `text_changes`

```python
text_changes: Tuple[dict, ...] = ()
```

### `to_dict`

```python
to_dict() -> dict
```

Return this slide's changes as a JSON-ready dict, omitting the categories that are empty.

## `SlideRef`

```python
SlideRef(slide_id: int, position: int, title: Optional[str]) -> None
```

Identifies a slide by id, position, and title.

### `position`

```python
position: int
```

### `slide_id`

```python
slide_id: int
```

### `title`

```python
title: Optional[str]
```

### `to_dict`

```python
to_dict() -> dict
```

Return this slide reference as a JSON-ready dict.

## `diff_decks`

```python
diff_decks(path_a, path_b, *, detail: str = 'structure') -> DeckDiff
```

Compare two decks; return the typed `DeckDiff`.

`path_a`/`path_b` accept a file path, a file-like object, or an already-open
`Presentation`. `detail`: "structure" (slide add/remove/move, shape add/remove,
geometry, image replacement, table-grid deltas), "text" (+ exact paragraph deltas,
chart data per series/category, notes), "full" (+ per-run effective-value shifts via
the resolver - expensive on large decks, deliberately opt-in).

When either side is an open `Presentation`, both sides are normalized by serializing
before the package-level comparison, because a live presentation has no on-disk package
to read; when both sides are a path or a file-like object, the packages are compared
exactly.

Matching is by permanent slide id and, within matched slides, top-level shapes match by
slide-wide shape id and compatible structural kind. Display names are labels only. Moving a
shape across a group boundary remains a top-level removal or addition. Independently built
decks can reuse ids and are outside this contract. A deleted shape id reused by a new
same-kind shape is likewise indistinguishable without a persistent identifier not present in
general PPTX files. Slide ids allocate as max+1, so deleting the highest-id slide and then
adding a new one RECYCLES the id, and this diff will read that delete-plus-add as one edited
slide - order add-before-delete when producing lineage decks you intend to diff.

Within each stable leaf-shape or table-frame container, paragraphs are ordered snapshot values
consisting of raw literal text plus positioned field markers. The longest exact prefix is
consumed first, followed by the longest non-overlapping exact suffix. The remaining middle is
one ``insertion``, ``deletion``, one-paragraph ``replacement``, or ``changed_region`` event.
Each event contains zero-based half-open ``before_range``/``after_range`` values and array-
valued ``before``/``after`` block evidence; every block carries its structured location, raw
text, and positioned fields. Duplicate values follow the prefix-first rule, which is a
deterministic endpoint representation rather than the historical edit location. Paragraph
moves and ambiguous identity are not inferred.

An unmatched text-bearing container produces one whole-container insertion or deletion event
and is never paired with another unmatched container.

Table dimensions are structural changes without inferred row/column insertion history.
At ``detail="full"``, effective-font and bullet shifts are limited to container-unique,
unchanged prefix/suffix paragraphs and exact unique run identities. Speaker notes remain one
flat comparison, while table-cell effective formatting and bullets remain outside the
supported resolver domain. ``package_changes`` remains the authoritative fallback for every
semantic package change not represented by a specialized facet.
