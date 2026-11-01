<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.inspect`

Effective-style inspection: resolved font values with provenance (paper-pptx).

## `BULLET_FOLLOWS_TEXT`

```python
BULLET_FOLLOWS_TEXT = 'follow text'
```

## `BlockAnchor`

```python
BlockAnchor(part: str, block_index: int, content_hash: str, version: Optional[int] = None, locator: Optional[dict] = None) -> None
```

A structural current anchor or conservative three-field legacy anchor.

Fields:

* ``part`` -- partname of the story the block lives in (a slide or notes slide).
* ``block_index`` -- diagnostic 0-based part traversal index. Current anchors never use it
  as write identity.
* ``content_hash`` -- a current full structural fingerprint, or the pinned eight-character
  text hash on a legacy anchor.
* ``version`` -- current anchor schema version; absent on legacy anchors.
* ``locator`` -- exact container identity and container-local paragraph coordinates.

Existing ``BlockAnchor(part, block_index, content_hash)`` construction remains valid and
creates a legacy anchor. Legacy writes search the named part for an exact unique hash match.

### `block_index`

```python
block_index: int
```

### `content_hash`

```python
content_hash: str
```

### `is_legacy`

```python
is_legacy: bool
```

Whether this is an old three-field anchor without structural identity.

### `locator`

```python
locator: Optional[dict] = None
```

### `part`

```python
part: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return this anchor as a JSON-ready dict. Store it to re-target the same text block on a
later open.

### `version`

```python
version: Optional[int] = None
```

## `DECK_MANIFEST_SCHEMA`

```python
DECK_MANIFEST_SCHEMA = 'paper-deck-manifest'
```

## `DECK_MANIFEST_VERSION`

```python
DECK_MANIFEST_VERSION = 1
```

## `DeckManifest`

```python
DeckManifest(slide_width: Optional[int], slide_height: Optional[int], slides: Tuple[SlideManifest, ...], masters: Tuple[dict, ...]) -> None
```

Structural survey of a whole deck. `.to_dict()` is deterministic (golden-tested).

### `masters`

```python
masters: Tuple[dict, ...]
```

### `slide_count`

```python
slide_count: int
```

Number of slides the manifest describes.

### `slide_height`

```python
slide_height: Optional[int]
```

### `slide_width`

```python
slide_width: Optional[int]
```

### `slides`

```python
slides: Tuple[SlideManifest, ...]
```

### `to_dict`

```python
to_dict() -> dict
```

Return the deck manifest as a JSON-ready dict stamped with its schema and version.

This is the payload to hand a caller that cannot hold Python objects.

## `EffectiveBullet`

```python
EffectiveBullet(type: Optional[str], char: Optional[str], number_scheme: Optional[str], start_at: Optional[int], resolved: bool, provenance: Tuple[ProvenanceStep, ...]) -> None
```

The bullet that actually renders on one paragraph, with its provenance chain.

### `char`

```python
char: Optional[str]
```

### `number_scheme`

```python
number_scheme: Optional[str]
```

### `provenance`

```python
provenance: Tuple[ProvenanceStep, ...]
```

### `resolved`

```python
resolved: bool
```

### `start_at`

```python
start_at: Optional[int]
```

### `to_dict`

```python
to_dict() -> dict
```

Return the resolved bullet as a JSON-ready dict, recording whether the answer came from
this paragraph or an inherited level.

### `type`

```python
type: Optional[str]
```

## `EffectiveFont`

```python
EffectiveFont(size: EffectiveValue, name: EffectiveValue, color_rgb: EffectiveValue, bold: EffectiveValue = None, italic: EffectiveValue = None, underline: EffectiveValue = None) -> None
```

Effective size, name, color, and emphasis of one run.

### `bold`

```python
bold: EffectiveValue = None
```

### `color_rgb`

```python
color_rgb: EffectiveValue
```

### `italic`

```python
italic: EffectiveValue = None
```

### `name`

```python
name: EffectiveValue
```

### `size`

```python
size: EffectiveValue
```

### `to_dict`

```python
to_dict() -> dict
```

Return the resolved font as a JSON-ready dict under the `paper-effective-font` schema.

Use it to report or diff typography without walking the inheritance chain again.

### `underline`

```python
underline: EffectiveValue = None
```

## `EffectiveParagraphFormat`

```python
EffectiveParagraphFormat(alignment: EffectiveValue, line_spacing: EffectiveValue, bullet: EffectiveBullet, bullet_font: EffectiveValue, bullet_size: EffectiveValue) -> None
```

Effective alignment, line spacing, and bullet of one paragraph (paper-pptx addition).

### `alignment`

```python
alignment: EffectiveValue
```

### `bullet`

```python
bullet: EffectiveBullet
```

### `bullet_font`

```python
bullet_font: EffectiveValue
```

### `bullet_size`

```python
bullet_size: EffectiveValue
```

### `line_spacing`

```python
line_spacing: EffectiveValue
```

### `to_dict`

```python
to_dict() -> dict
```

Return the resolved paragraph format as a JSON-ready dict under the `paper-effective-
paragraph-format` schema.

## `EffectiveShapeFormat`

```python
EffectiveShapeFormat(fill_rgb: EffectiveValue, line_rgb: EffectiveValue) -> None
```

Effective solid fill and line color of one shape (paper-pptx addition).

It resolves EXPLICIT `p:spPr` fills fully (solid colors through the scheme/clrMap/
theme walk; "none" for noFill). A shape whose fill comes only from its `p:style`
fill/line reference reports unresolved — the provenance carries the reference index and
its resolved phClr color, but theme format-scheme modulation is not applied (guessing it
would violate fail-loudly).

### `fill_rgb`

```python
fill_rgb: EffectiveValue
```

### `line_rgb`

```python
line_rgb: EffectiveValue
```

### `to_dict`

```python
to_dict() -> dict
```

Return the resolved fill and line colors as a JSON-ready dict.

## `EffectiveValue`

```python
EffectiveValue(value: object, value_pt: Optional[float], resolved: bool, provenance: Tuple[ProvenanceStep, ...], bake_color_xml: Optional[bytes] = None) -> None
```

A resolved (or honestly-unresolved) effective value with its provenance chain.

### `bake_color_xml`

```python
bake_color_xml: Optional[bytes] = field(default=None, repr=False, compare=False)
```

### `provenance`

```python
provenance: Tuple[ProvenanceStep, ...]
```

### `resolved`

```python
resolved: bool
```

### `to_dict`

```python
to_dict() -> dict
```

Return this resolved value as a JSON-ready dict, provenance trail included.

### `value`

```python
value: object
```

### `value_pt`

```python
value_pt: Optional[float]
```

## `InspectedRun`

```python
InspectedRun(text: str, font: EffectiveFont, field_type: Optional[str] = None) -> None
```

One run of text paired with the font that renders it.

### `field_type`

```python
field_type: Optional[str] = None
```

### `font`

```python
font: EffectiveFont
```

### `text`

```python
text: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return this run as a JSON-ready dict, omitting `field_type` for ordinary text.

## `ProvenanceStep`

```python
ProvenanceStep(level: str, part: Optional[str], detail: str, supplied: bool) -> None
```

One consulted level of an inheritance walk.

### `detail`

```python
detail: str
```

### `level`

```python
level: str
```

### `part`

```python
part: Optional[str]
```

### `supplied`

```python
supplied: bool
```

### `to_dict`

```python
to_dict() -> dict
```

Return this rung as a JSON-ready dict, for reports that show where a value came from.

## `SCHEMA_NAME`

```python
SCHEMA_NAME = 'paper-text-inspection'
```

## `SCHEMA_VERSION`

```python
SCHEMA_VERSION = 3
```

## `ShapeManifest`

```python
ShapeManifest(shape_id: int, name: str, kind: str, z_index: int, placeholder_type: Optional[str], x: Optional[int], y: Optional[int], cx: Optional[int], cy: Optional[int], rotation: Optional[float], text_block_count: int, autofit: Optional[dict], table: Optional[dict], chart: Optional[dict], image: Optional[dict], children: Tuple['ShapeManifest', ...] = ()) -> None
```

Structural facts of one shape (paper-pptx addition). Group children nest.

### `autofit`

```python
autofit: Optional[dict]
```

### `chart`

```python
chart: Optional[dict]
```

### `children`

```python
children: Tuple['ShapeManifest', ...] = ()
```

### `cx`

```python
cx: Optional[int]
```

### `cy`

```python
cy: Optional[int]
```

### `image`

```python
image: Optional[dict]
```

### `kind`

```python
kind: str
```

### `name`

```python
name: str
```

### `placeholder_type`

```python
placeholder_type: Optional[str]
```

### `rotation`

```python
rotation: Optional[float]
```

### `shape_id`

```python
shape_id: int
```

### `table`

```python
table: Optional[dict]
```

### `text_block_count`

```python
text_block_count: int
```

### `to_dict`

```python
to_dict() -> dict
```

Return this shape's manifest entry as a JSON-ready dict.

### `x`

```python
x: Optional[int]
```

### `y`

```python
y: Optional[int]
```

### `z_index`

```python
z_index: int
```

## `SlideManifest`

```python
SlideManifest(part: str, slide_id: int, layout_name: str, has_notes: bool, shapes: Tuple[ShapeManifest, ...], alternate_content_count: int = 0) -> None
```

Structural facts of one slide.

### `alternate_content_count`

```python
alternate_content_count: int = 0
```

### `has_notes`

```python
has_notes: bool
```

### `layout_name`

```python
layout_name: str
```

### `part`

```python
part: str
```

### `shapes`

```python
shapes: Tuple[ShapeManifest, ...]
```

### `slide_id`

```python
slide_id: int
```

### `to_dict`

```python
to_dict() -> dict
```

Return this slide's manifest entry as a JSON-ready dict, shapes included.

## `TextBlock`

```python
TextBlock(anchor: BlockAnchor, shape_id: int, shape_name: str, placeholder_type: Optional[str], level: int, text: str, runs: Tuple[InspectedRun, ...], container: str = 'shape', container_detail: Optional[str] = None, blind: bool = False, fields: Tuple[str, ...] = ()) -> None
```

One paragraph of one text body, with anchor and per-run effective fonts.

`container` says where the text lives: "shape" (a top-level `p:sp`), "group" (a `p:sp`
inside `p:grpSp` nesting; `container_detail` is the slash-joined group-name path), or
"table-cell" (`container_detail` is `"<frame-name>!r{row}c{col}"`). `blind` is True when
the block's *text* is visible but its effective values are unresolvable by design in
this version (table-cell runs inherit through table styles, a chain not yet walked);
a blind block's runs carry `resolved=False` values, never guesses.

### `anchor`

```python
anchor: BlockAnchor
```

### `blind`

```python
blind: bool = False
```

### `container`

```python
container: str = 'shape'
```

### `container_detail`

```python
container_detail: Optional[str] = None
```

### `fields`

```python
fields: Tuple[str, ...] = ()
```

### `level`

```python
level: int
```

### `placeholder_type`

```python
placeholder_type: Optional[str]
```

### `runs`

```python
runs: Tuple[InspectedRun, ...]
```

### `shape_id`

```python
shape_id: int
```

### `shape_name`

```python
shape_name: str
```

### `text`

```python
text: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return this block as a JSON-ready dict, anchor included, for reports and diffs.

## `TextInspection`

```python
TextInspection(part: str, blocks: Tuple[TextBlock, ...] = tuple()) -> None
```

Inspection payload for one slide. `.to_dict()` is deterministic (golden-tested).

### `blind_region_count`

```python
blind_region_count: int
```

Number of blocks whose effective values are unresolvable by design (see TextBlock).

### `blocks`

```python
blocks: Tuple[TextBlock, ...] = field(default_factory=tuple)
```

### `part`

```python
part: str
```

### `to_dict`

```python
to_dict() -> dict
```

Return the whole inspection as a JSON-ready dict stamped with its schema name and
version.

Hand this to anything outside Python that needs the text of a part.

## `content_hash`

```python
content_hash(text: str) -> str
```

First 8 hex chars of SHA-256 over the NFC-normalized text (the pinned anchor hash).

Unicode normalization only — whitespace is content and is never trimmed.

## `effective_font`

```python
effective_font(run: '_Run') -> EffectiveFont
```

Return the `EffectiveFont` for `run`, a `_Run` on a slide shape.

Raises `UnsupportedStructureError` for runs outside a `p:sp` shape on a slide part
(table-cell and chart text are not resolved).

## `effective_paragraph_format`

```python
effective_paragraph_format(paragraph) -> EffectiveParagraphFormat
```

Return effective alignment, line spacing, and bullet for `paragraph`, with provenance.

Same inheritance walk as `effective_font`, over paragraph-level properties: the
paragraph's own `a:pPr`, the shape's `lstStyle` level entry, the placeholder chain (or
the presentation's `defaultTextStyle`). Exhaustion resolves rather than reporting
unresolved: to the ECMA-376 schema default for alignment (left), and to the rendering
conventions for line spacing (single), bullet (no bullet), and bullet typeface/size
(follow the text), none of which the schema defines.

The bullet's kind, typeface, and size are three separate XSD choice groups that inherit
independently, so each is walked on its own and carries its own provenance. A paragraph
can take its `buChar` from the master and its `buSzPct` from the layout.

## `effective_shape_format`

```python
effective_shape_format(shape) -> EffectiveShapeFormat
```

Return the effective solid fill and line color of `shape`, with provenance.

## `inspect_deck`

```python
inspect_deck(prs) -> DeckManifest
```

Return a structural `DeckManifest` of `prs` (paper-pptx addition).

The survey every brownfield edit starts with, as one deterministic typed payload:
per-slide shape inventory (identity, kind, z-order, geometry where explicit, placeholder
role, table/chart/image/autofit facts), group children nested, layout and master
inventory. Values that are inherited rather than explicit report None — never a guess.
Read-only.

## `inspect_text`

```python
inspect_text(slide: 'Slide') -> TextInspection
```

Return a `TextInspection` of every text block on `slide`, visibility-complete.

Traversal is depth-first document order over the shape tree: top-level `p:sp` shapes,
`p:sp` shapes inside groups (recursively, to any depth), and table cells
(row-major within each table graphic-frame). `block_index` numbers blocks consecutively
in that pinned order for diagnostics only; current anchors address their owning shape or
table cell structurally. Table-cell blocks report their text but are *blind regions* for
effective values (see `TextBlock`); chart text lives in the chart part, not the slide
part, and is out of scope here.

## `iter_text_bodies`

```python
iter_text_bodies(spTree)
```

Yield `(kind, owner_elm, txBody, group_path, cell_coordinates)` in document order.

The single source of traversal truth shared by `inspect_text` and `pptx.edit`
(visibility-complete: top-level `p:sp`, grouped `p:sp` recursively to any depth,
table cells row-major). `kind` is "shape" | "group" | "table-cell"; `owner_elm` is the
`p:sp` or `p:graphicFrame`; `cell_coordinates` is ``(row, column)`` for table cells and
`None` otherwise. Display-oriented cell detail is assembled only by inspection callers.
