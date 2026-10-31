<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.parts.slide`

Slide and related objects.

## `BaseSlidePart`

Bases: `XmlPart`

Base class for slide parts.

This includes slide, slide-layout, and slide-master parts, but also notes-slide,
notes-master, and handout-master parts.

### `get_image`

```python
get_image(rId: str) -> Image
```

Return an `Image` object containing the image related to this slide by *rId*.

Raises `KeyError` if no image is related by that id, which would generally indicate a
corrupted .pptx file.

### `get_or_add_image_part`

```python
get_or_add_image_part(image_file: str | IO[bytes])
```

Return `(image_part, rId)` pair corresponding to `image_file`.

The returned `ImagePart` object contains the image in `image_file` and is
related to this slide with the key `rId`. If either the image part or
relationship already exists, they are reused, otherwise they are newly created.

### `name`

```python
name: str
```

Internal name of this slide.

## `NotesMasterPart`

Bases: `BaseSlidePart`

Notes master part.

Corresponds to package file `ppt/notesMasters/notesMaster1.xml`.

### `create_default`

```python
create_default(package)
```

Create and return a default notes master part, including creating the
new theme it requires.

### `notes_master`

```python
notes_master()
```

Return the `NotesMaster` object that proxies this notes master part.

## `NotesSlidePart`

Bases: `BaseSlidePart`

Notes slide part.

Contains the slide notes content and the layout for the slide handout page.
Corresponds to package file `ppt/notesSlides/notesSlide[1-9][0-9]*.xml`.

### `new`

```python
new(package, slide_part)
```

Return new `NotesSlidePart` for the slide in `slide_part`.

The new notes-slide part is based on the (singleton) notes master and related to
both the notes-master part and `slide_part`. If no notes-master is present,
one is created based on the default template.

### `notes_master`

```python
notes_master()
```

Return the `NotesMaster` object this notes slide inherits from.

### `notes_slide`

```python
notes_slide()
```

Return the `NotesSlide` object that proxies this notes slide part.

## `SlideLayoutPart`

Bases: `BaseSlidePart`

Slide layout part.

Corresponds to package files ``ppt/slideLayouts/slideLayout[1-9][0-9]*.xml``.

### `slide_layout`

```python
slide_layout()
```

The `SlideLayout` object representing this part.

### `slide_master`

```python
slide_master: SlideMaster
```

Slide master from which this slide layout inherits properties.

## `SlideMasterPart`

Bases: `BaseSlidePart`

Slide master part.

Corresponds to package files ppt/slideMasters/slideMaster[1-9][0-9]*.xml.

### `related_slide_layout`

```python
related_slide_layout(rId: str) -> SlideLayout
```

Return `SlideLayout` related to this slide-master by key `rId`.

### `slide_master`

```python
slide_master()
```

The `SlideMaster` object representing this part.

## `SlidePart`

Bases: `BaseSlidePart`

Slide part. Corresponds to package files ppt/slides/slide[1-9][0-9]*.xml.

### `add_chart_part`

```python
add_chart_part(chart_type: XL_CHART_TYPE, chart_data: ChartData)
```

Return str rId of new `ChartPart` object containing chart of `chart_type`.

The chart depicts `chart_data` and is related to the slide contained in this
part by `rId`.

### `add_embedded_ole_object_part`

```python
add_embedded_ole_object_part(prog_id: PROG_ID | str, ole_object_file: str | IO[bytes])
```

Return rId of newly-added OLE-object part formed from `ole_object_file`.

### `get_or_add_video_media_part`

```python
get_or_add_video_media_part(video: Video) -> tuple[str, str]
```

Return rIds for media and video relationships to media part.

A new `MediaPart` object is created if it does not already exist
(such as would occur if the same video appeared more than once in
 a presentation). Two relationships to the media part are created,
one each with MEDIA and VIDEO relationship types. The need for two
appears to be for legacy support for an earlier (pre-Office 2010)
PowerPoint media embedding strategy.

### `has_notes_slide`

```python
has_notes_slide
```

Return True if this slide has a notes slide, False otherwise. A notes
slide is created by the `notes_slide` property when one doesn't
exist; use this property to test for a notes slide without the
possible side-effect of creating one.

### `new`

```python
new(partname, package, slide_layout_part)
```

Return newly-created blank slide part.

The new slide-part has `partname` and a relationship to `slide_layout_part`.

### `notes_slide`

```python
notes_slide() -> NotesSlide
```

The `NotesSlide` instance associated with this slide.

If the slide does not have a notes slide, a new one is created. The same single instance
is returned on each call.

### `slide`

```python
slide()
```

The `Slide` object representing this slide part.

### `slide_id`

```python
slide_id: int
```

Return the slide identifier stored in the presentation part for this slide part.

### `slide_layout`

```python
slide_layout: SlideLayout
```

`SlideLayout` object the slide in this part inherits appearance from.
