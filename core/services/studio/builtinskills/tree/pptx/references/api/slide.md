<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.slide`

Slide-related objects, including masters, layouts, and notes.

## `HeaderFooters`

```python
HeaderFooters(owner)
```

Bases: `object`

Header/footer placeholder visibility flags of a layout or master (paper-pptx addition).

Wraps the `p:hf` element. Each property is tri-state: `True`/`False` when the attribute
is explicit, `None` when it is absent — meaning "inherit" (a layout inherits from its
master; the schema default is visible). Assigning `None` removes the attribute.

Bind to the layout or master that owns these flags.

### `date_visible`

```python
date_visible: bool | None
```

Visibility of the date placeholder (`p:hf/@dt`).

### `footer_visible`

```python
footer_visible: bool | None
```

Visibility of the footer placeholder (`p:hf/@ftr`).

### `slide_number_visible`

```python
slide_number_visible: bool | None
```

Visibility of the slide-number placeholder (`p:hf/@sldNum`).

## `NotesMaster`

Bases: `_BaseMaster`

Proxy for the notes master XML document.

Provides access to shapes, the most commonly used of which are placeholders.

## `NotesSlide`

Bases: `_BaseSlide`

Notes slide object.

Provides access to slide notes placeholder and other shapes on the notes handout
page.

### `clone_master_placeholders`

```python
clone_master_placeholders(notes_master: NotesMaster) -> None
```

Selectively add placeholder shape elements from `notes_master`.

Selected placeholder shape elements from `notes_master` are added to the shapes
collection of this notes slide. Z-order of placeholders is preserved. Certain
placeholders (header, date, footer) are not cloned.

### `element`

```python
element: CT_NotesSlide
```

### `notes_placeholder`

```python
notes_placeholder: NotesSlidePlaceholder | None
```

the notes placeholder on this notes slide, the shape that contains the actual notes text.

Return `None` if no notes placeholder is present; while this is probably uncommon, it can
happen if the notes master does not have a body placeholder, or if the notes placeholder
has been deleted from the notes slide.

### `notes_text_frame`

```python
notes_text_frame: TextFrame | None
```

The text frame of the notes placeholder on this notes slide.

`None` if there is no notes placeholder. This is a shortcut to accommodate the common case
of simply adding "notes" text to the notes "page".

### `placeholders`

```python
placeholders() -> NotesSlidePlaceholders
```

Instance of `NotesSlidePlaceholders` for this notes-slide.

Contains the sequence of placeholder shapes in this notes slide.

### `shapes`

```python
shapes() -> NotesSlideShapes
```

Sequence of shape objects appearing on this notes slide.

## `Slide`

Bases: `_BaseSlide`

Slide object. Provides access to shapes and slide-level properties.

### `apply_footers`

```python
apply_footers(*, footer: str | None = None, slide_number: bool = False, date_format: str | None = None, fixed_date: str | None = None, now: 'datetime | None' = None) -> None
```

Apply the complete footer state to this slide only (the dialog's "Apply").

paper-pptx addition. Same parameters, mechanism, and refusals as
`.Presentation.apply_footers`, restricted to this slide — the per-slide
override path (e.g. removing just this slide's footer while the rest of the deck
keeps it). Each call sets this slide's full three-element state.

### `follow_master_background`

```python
follow_master_background
```

`True` if this slide inherits the slide master background.

Assigning `False` causes background inheritance from the master to be
interrupted; if there is no custom background for this slide,
a default background is added. If a custom background already exists
for this slide, assigning `False` has no effect.

Assigning `True` causes any custom background for this slide to be
deleted and inheritance from the master restored.

### `has_notes_slide`

```python
has_notes_slide: bool
```

`True` if this slide has a notes slide, `False` otherwise.

A notes slide is created by `.notes_slide` when one doesn't exist; use this property
to test for a notes slide without the possible side effect of creating one.

### `notes_slide`

```python
notes_slide: NotesSlide
```

The `NotesSlide` instance for this slide.

If the slide does not have a notes slide, one is created. The same single instance is
returned on each call.

### `part`

```python
part: SlidePart
```

### `placeholders`

```python
placeholders() -> SlidePlaceholders
```

Sequence of placeholder shapes in this slide.

### `read_notes_text`

```python
read_notes_text() -> str
```

Return the text of this slide's existing speaker notes.

paper-pptx addition. Unlike `notes_slide`, this NEVER creates a notes slide:
a slide with no notes part raises `UnsupportedStructureError` (as does a notes slide
with no body placeholder). Returns "" for an empty existing notes body.

### `rebind_layout`

```python
rebind_layout(target_layout: 'SlideLayout', *, placeholder_map: 'SlideLayout' = 'auto', orphan_policy: str = 'refuse') -> 'RebindReport'
```

Move this slide to `target_layout`; return the required `RebindReport`.

paper-pptx addition — the template-migration *primitive* (bulk-migration
workflows are left to the caller). Placeholders reconcile against the
target layout: auto-matching binds by exact type+idx, then same type, then
interchangeable type family (title/ctrTitle; body/object/subTitle). The two
fallback tiers bind only when exactly one unclaimed target exists; ambiguity
refuses and requires an explicit map. Pass
`placeholder_map={source_idx: target_idx | None}` to override any of it (None
force-orphans a source). Source placeholders with no destination follow
`orphan_policy`: "refuse" (default; typed, atomic) or "bake" — convert to a free
shape with inherited geometry materialized and each run's *resolved* effective
formatting written locally, so the text keeps its look.

The report is not optional: the effective-value resolver runs before and after,
and every run whose resolved values changed appears with its before/after payloads
— a rebind never shifts appearance silently. Same-package only (cross-package
composition is `import_slide`'s job); slides carrying `mc:AlternateContent`
refuse (shapes inside are invisible to reconciliation).

### `replace_notes_text`

```python
replace_notes_text(text: str) -> None
```

Replace the text of this slide's existing speaker notes with `text`.

paper-pptx addition. Only the notes *body* placeholder is touched — slide-number and
other notes placeholders are preserved untouched. The first paragraph's properties
and its first run's character formatting are kept and applied to the replacement
text; `"\n"` in `text` starts a new paragraph. Never creates a notes slide: a slide
with no notes part raises `UnsupportedStructureError` before anything changes
(creating the notes part graph is intentionally not supported).

### `shapes`

```python
shapes() -> SlideShapes
```

Sequence of shape objects appearing on this slide.

### `slide_id`

```python
slide_id: int
```

Integer value that uniquely identifies this slide within this presentation.

The slide id does not change if the position of this slide in the slide sequence is changed
by adding, rearranging, or deleting slides.

### `slide_layout`

```python
slide_layout: SlideLayout
```

`SlideLayout` object this slide inherits appearance from.

## `SlideClonePolicy`

```python
SlideClonePolicy(deep_copy_charts: bool = True, deep_copy_notes: bool = True, share_media: bool = True) -> None
```

Relationship policy for `Slides.clone` (paper-pptx addition).

Defaults encode the production-proven policy: charts (with their embedded workbooks and
style parts) and speaker notes are deep-copied so clone and original can never
cross-contaminate; image/media parts are shared deliberately.

- `deep_copy_charts`: must be True to clone a slide bearing charts; False refuses
  (`RelationshipPolicyError`) rather than share an editable chart part between slides.
- `deep_copy_notes`: False drops the notes slide from the clone (original unaffected).
- `share_media`: False deep-copies image/media parts instead of sharing them.

### `deep_copy_charts`

```python
deep_copy_charts: bool = True
```

### `deep_copy_notes`

```python
deep_copy_notes: bool = True
```

### `share_media`

```python
share_media: bool = True
```

## `SlideLayout`

Bases: `_BaseSlide`

Slide layout object.

Provides access to placeholders, regular shapes, and slide layout-level properties.

### `header_footers`

```python
header_footers: HeaderFooters
```

`HeaderFooters` flags for this layout (paper-pptx addition).

### `iter_cloneable_placeholders`

```python
iter_cloneable_placeholders() -> Iterator[LayoutPlaceholder]
```

Generate layout-placeholders on this slide-layout that should be cloned to a new slide.

Used when creating a new slide from this slide-layout.

### `part`

```python
part: SlideLayoutPart
```

### `placeholders`

```python
placeholders() -> LayoutPlaceholders
```

Sequence of placeholder shapes in this slide layout.

Placeholders appear in `idx` order.

### `shapes`

```python
shapes() -> LayoutShapes
```

Sequence of shapes appearing on this slide layout.

### `slide_master`

```python
slide_master: SlideMaster
```

Slide master from which this slide-layout inherits properties.

### `used_by_slides`

```python
used_by_slides
```

Tuple of slide objects based on this slide layout.

## `SlideLayouts`

```python
SlideLayouts(sldLayoutIdLst: CT_SlideLayoutIdList, parent: SlideMaster)
```

Bases: `ParentedElementProxy`

Sequence of slide layouts belonging to a slide-master.

Supports indexed access, len(), iteration, index() and remove().

### `get_by_name`

```python
get_by_name(name: str, default: SlideLayout | None = None) -> SlideLayout | None
```

Return SlideLayout object having `name`, or `default` if not found.

### `index`

```python
index(slide_layout: SlideLayout) -> int
```

Return zero-based index of `slide_layout` in this collection.

Raises `ValueError` if `slide_layout` is not present in this collection.

### `part`

```python
part: SlideMasterPart
```

### `remove`

```python
remove(slide_layout: SlideLayout) -> None
```

Remove `slide_layout` from the collection.

Raises ValueError when `slide_layout` is in use; a slide layout which is the basis for one
or more slides cannot be removed.

Refuses with TargetNotFoundError when the layout belongs to another presentation, and with
UnsupportedStructureError when its part carries inbound relationships beyond this
collection's own, where dropping it would strand whatever else points at it.

## `SlideMaster`

Bases: `_BaseMaster`

Slide master object.

Provides access to slide layouts. Access to placeholders, regular shapes, and slide master-level
properties is inherited from `_BaseMaster`.

### `header_footers`

```python
header_footers: HeaderFooters
```

`HeaderFooters` flags for this master (paper-pptx addition).

### `slide_layouts`

```python
slide_layouts() -> SlideLayouts
```

`SlideLayouts` object providing access to this slide-master's layouts.

## `SlideMasters`

```python
SlideMasters(sldMasterIdLst: CT_SlideMasterIdList, parent: Presentation)
```

Bases: `ParentedElementProxy`

Sequence of `SlideMaster` objects belonging to a presentation.

Has list access semantics, supporting indexed access, len(), and iteration.

### `part`

```python
part: PresentationPart
```

## `Slides`

```python
Slides(sldIdLst: CT_SlideIdList, prs: Presentation)
```

Bases: `ParentedElementProxy`

Sequence of slides belonging to an instance of `Presentation`.

Has list semantics for access to individual slides. Supports indexed access, len(), and
iteration.

### `add_slide`

```python
add_slide(slide_layout: SlideLayout) -> Slide
```

Return a newly added slide that inherits layout from `slide_layout`.

### `clone`

```python
clone(source: Slide | int, *, after: Slide | int | None = None, policy: SlideClonePolicy | None = None) -> Slide
```

Return a new slide that is a policy-governed deep copy of `source`.

paper-pptx addition. The clone's relationship graph follows `policy` (default
`SlideClonePolicy`): layout shared; charts deep-copied WITH their embedded workbooks
and style parts; notes deep-copied and re-linked to the clone; image/media shared;
external (hyperlink) relationships copied. A slide bearing any other relationship
type (OLE objects, controls, SmartArt, comments, …) refuses with
`RelationshipPolicyError` before anything changes.

The clone is inserted directly after `source`, or after the slide given by `after`.
`source`/`after` accept a `Slide` or a 0-based index; a `Slide` from another
presentation raises `TargetNotFoundError`.

### `delete`

```python
delete(slide: Slide | int) -> None
```

Remove `slide` from this presentation.

paper-pptx addition. Removes the slide's `p:sldId` entry and the presentation's
relationship to the slide part; parts then unreachable through the relationship
graph (the slide, and e.g. its charts and notes if unshared) are never serialized
again — orphans structurally cannot reach disk. Deleting the last slide is allowed.

### `get`

```python
get(slide_id: int, default: Slide | None = None) -> Slide | None
```

Return the slide identified by int `slide_id` in this presentation.

Returns `default` if not found.

### `index`

```python
index(slide: Slide) -> int
```

Map `slide` to its zero-based position in this slide sequence.

Raises `ValueError` on *slide* not present.

### `move`

```python
move(slide: Slide | int, to_index: int) -> None
```

Move `slide` so it sits at 0-based `to_index` in the slide sequence.

paper-pptx addition. `to_index` outside `range(len(slides))` raises `ValueError`.

### `part`

```python
part: PresentationPart
```

### `reorder`

```python
reorder(new_order: Sequence[int]) -> None
```

Permute the slide sequence: new position i shows the slide now at `new_order[i]`.

paper-pptx addition. `new_order` must be an exact permutation of
`range(len(slides))`; anything else raises `ValueError` before any change.
