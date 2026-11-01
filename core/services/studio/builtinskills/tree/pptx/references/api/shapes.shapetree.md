<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes.shapetree`

The shape tree, the structure that holds a slide's shapes.

## `BasePlaceholders`

Bases: `_BaseShapes`

Base class for placeholder collections.

Subclasses differentiate behaviors for a master, layout, and slide. By default, placeholder
shapes are constructed using `BaseShapeFactory`. Subclasses should override
:method:`_shape_factory` to use custom placeholder classes.

## `BaseShapeFactory`

```python
BaseShapeFactory(shape_elm: ShapeElement, parent: ProvidesPart) -> BaseShape
```

Return an instance of the appropriate shape proxy class for `shape_elm`.

## `GroupShapes`

Bases: `_BaseGroupShapes`

The sequence of child shapes belonging to a group shape.

Note that this collection can itself contain a group shape, making this part of a recursive,
tree data structure (acyclic graph).

## `LayoutPlaceholders`

Bases: `BasePlaceholders`

Sequence of `LayoutPlaceholder` instance for each placeholder shape on a slide layout.

### `get`

```python
get(idx: int, default: LayoutPlaceholder | None = None) -> LayoutPlaceholder | None
```

The first placeholder shape with matching `idx` value, or `default` if not found.

## `LayoutShapes`

Bases: `_BaseShapes`

Sequence of shapes appearing on a slide layout.

The first shape in the sequence is the backmost in z-order and the last shape is topmost.
Supports indexed access, len(), index(), and iteration.

## `MasterPlaceholders`

Bases: `BasePlaceholders`

Sequence of MasterPlaceholder representing the placeholder shapes on a slide master.

### `get`

```python
get(ph_type: PP_PLACEHOLDER, default: MasterPlaceholder | None = None)
```

Return the first placeholder shape with type `ph_type` (e.g. 'body').

Returns `default` if no such placeholder shape is present in the collection.

## `MasterShapes`

Bases: `_BaseShapes`

Sequence of shapes appearing on a slide master.

The first shape in the sequence is the backmost in z-order and the last shape is topmost.
Supports indexed access, len(), and iteration.

## `NotesSlidePlaceholders`

Bases: `MasterPlaceholders`

Sequence of placeholder shapes on a notes slide.

## `NotesSlideShapes`

Bases: `_BaseShapes`

Sequence of shapes appearing on a notes slide.

The first shape in the sequence is the backmost in z-order and the last shape is topmost.
Supports indexed access, len(), index(), and iteration.

### `ph_basename`

```python
ph_basename(ph_type: PP_PLACEHOLDER) -> str
```

Return the base name for a placeholder of `ph_type` in this shape collection.

A notes slide uses a different name for the body placeholder and has some unique
placeholder types, so this method overrides the default in the base class.

## `SlidePlaceholders`

Bases: `ParentedElementProxy`

Collection of placeholder shapes on a slide.

Supports iteration, `len`, and dictionary-style lookup on the `idx` value of the
placeholders it contains.

## `SlideShapeFactory`

```python
SlideShapeFactory(shape_elm: ShapeElement, parent: ProvidesPart) -> BaseShape
```

Return appropriate shape object for `shape_elm` on a slide.

## `SlideShapes`

Bases: `_BaseGroupShapes`

Sequence of shapes appearing on a slide.

The first shape in the sequence is the backmost in z-order and the last shape is topmost.
Supports indexed access, len(), index(), and iteration.

### `add_copy`

```python
add_copy(shape)
```

Return a copy of `shape` (from this or another slide) added to this slide.

paper-pptx addition. The copy gets fresh shape ids; its
relationships follow the pinned policy: image/media parts shared, external
(hyperlink) relationships copied, charts deep-copied WITH their embedded workbooks
and style parts. Any other relationship type refuses with `RelationshipPolicyError`
before anything changes. A shape from another presentation raises
`TargetNotFoundError`.

### `add_movie`

```python
add_movie(movie_file: str | IO[bytes], left: Length, top: Length, width: Length, height: Length, poster_frame_image: str | IO[bytes] | None = None, mime_type: str = CT.VIDEO) -> GraphicFrame
```

Return newly added movie shape displaying video in `movie_file`.

**EXPERIMENTAL.** This method has important limitations:

* The size must be specified; no auto-scaling such as that provided by `add_picture`
  is performed.
* The MIME type of the video file should be specified, e.g. 'video/mp4'. The provided
  video file is not interrogated for its type. The MIME type `video/unknown` is used by
  default (and works fine in tests as of this writing).
* A poster frame image must be provided, it cannot be automatically extracted from the
  video file. If no poster frame is provided, the default "media loudspeaker" image will
  be used.

Return a newly added movie shape to the slide, positioned at (`left`, `top`), having size
(`width`, `height`), and containing `movie_file`. Before the video is started,
`poster_frame_image` is displayed as a placeholder for the video.

### `add_table`

```python
add_table(rows: int, cols: int, left: Length, top: Length, width: Length, height: Length) -> GraphicFrame
```

Add a `GraphicFrame` object containing a table.

The table has the specified number of `rows` and `cols` and the specified position and
size. `width` is evenly distributed between the columns of the new table. Likewise,
`height` is evenly distributed between the rows. Note that the `.table` property on the
returned `GraphicFrame` shape must be used to access the enclosed `Table` object.

### `chart_by_name`

```python
chart_by_name(name: str)
```

Return the `Chart` held by the shape on this slide named `name`.

paper-pptx addition, the chart-addressing half of safe chart-data replacement.
Group-aware: shapes inside groups are found too. Raises
`TargetNotFoundError` when no shape has that name, or when shapes with the name
exist but none holds a chart (the message says what was found instead). Raises
`AmbiguousTargetError` when more than one chart-bearing shape has the name — this
API never guesses between them.

### `clone_layout_placeholders`

```python
clone_layout_placeholders(slide_layout: SlideLayout) -> None
```

Add placeholder shapes based on those in `slide_layout`.

Z-order of placeholders is preserved. Latent placeholders (date, slide number, and footer)
are not cloned.

### `delete`

```python
delete(shape) -> None
```

Remove `shape` from this slide, with relationship hygiene.

paper-pptx addition. Relationships referenced by the removed
subtree are dropped unless something else in the part still references them (two
pictures can share one image relationship). A shape that is not a direct member of
this collection — including a shape inside a group — raises `TargetNotFoundError`
(delete the group, or ungroup first).

### `move`

```python
move(shape, to_index: int) -> None
```

Move `shape` to 0-based `to_index` in this collection's z-order.

paper-pptx addition. Index 0 is backmost, the last index topmost —
the same order this collection iterates. `to_index` outside range raises
`ValueError`; a shape not directly in this collection raises `TargetNotFoundError`.

### `parent`

```python
parent: Slide
```

### `picture_by_name`

```python
picture_by_name(name: str)
```

Return the `Picture` on this slide named `name` (group-aware).

paper-pptx addition, with the same contract as `chart_by_name`:
`TargetNotFoundError` when nothing (or nothing picture-shaped) has the name,
`AmbiguousTargetError` when several pictures do.

### `placeholders`

```python
placeholders: SlidePlaceholders
```

Sequence of placeholder shapes in this slide.

### `shape_by_name`

```python
shape_by_name(name: str)
```

Return the single shape on this slide named `name` (group-aware).

paper-pptx addition: `TargetNotFoundError` / `AmbiguousTargetError`,
never first-match.

### `table_by_name`

```python
table_by_name(name: str)
```

Return the `Table` held by the graphic frame on this slide named `name`.

paper-pptx addition, same contract as `chart_by_name`.

### `title`

```python
title: Shape | None
```

The title placeholder shape on the slide.

`None` if the slide has no title placeholder.
