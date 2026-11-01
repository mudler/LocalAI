<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.parts.presentation`

Presentation part, the main part in a .pptx package.

## `PresentationPart`

Bases: `XmlPart`

Top level class in object model.

Represents the contents of the /ppt directory of a .pptx file.

### `add_slide`

```python
add_slide(slide_layout: SlideLayout)
```

Return (rId, slide) pair of a newly created blank slide.

New slide inherits appearance from `slide_layout`.

### `core_properties`

```python
core_properties: CorePropertiesPart
```

A `CoreProperties` object for the presentation.

Provides read/write access to the Dublin Core properties of this presentation.

### `get_slide`

```python
get_slide(slide_id: int) -> Slide | None
```

Return optional related `Slide` object identified by `slide_id`.

Returns `None` if no slide with `slide_id` is related to this presentation.

### `notes_master`

```python
notes_master() -> NotesMaster
```

Return the `NotesMaster` object for this presentation. If the
presentation does not have a notes master, one is created from
a default template. The same single instance is returned on each
call.

### `notes_master_part`

```python
notes_master_part() -> NotesMasterPart
```

Return the `NotesMasterPart` object for this presentation.

If the presentation does not have a notes master, one is created from a default template.
The same single instance is returned on each call.

### `presentation`

```python
presentation()
```

A `Presentation` object providing access to the content of this
presentation.

### `related_slide`

```python
related_slide(rId: str) -> Slide
```

Return `Slide` object for related `SlidePart` related by `rId`.

### `related_slide_master`

```python
related_slide_master(rId: str) -> SlideMaster
```

Return `SlideMaster` object for `SlideMasterPart` related by `rId`.

### `rename_slide_parts`

```python
rename_slide_parts(rIds: Iterable[str])
```

Assign incrementing partnames to the slide parts identified by `rIds`.

Partnames are like `/ppt/slides/slide9.xml` and are assigned in the order their id appears
in the `rIds` sequence. The name portion is always `slide`. The number part forms a
continuous sequence starting at 1 (e.g. 1, 2, ... 10, ...). The extension is always
`.xml`.

### `save`

```python
save(path_or_stream: str | IO[bytes])
```

Save this presentation package to `path_or_stream`.

`path_or_stream` can be either a path to a filesystem location (a string) or a
file-like object. A pass-through; `pptx.opc.package.OpcPackage.save` carries
the write contract.

### `slide_id`

```python
slide_id(slide_part)
```

Return the slide-id associated with `slide_part`.
