<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.slide`

Slide-related custom element classes, including those for masters.

## `CT_Background`

Bases: `BaseOxmlElement`

`p:bg` element.

### `add_noFill_bgPr`

```python
add_noFill_bgPr()
```

Return a new `p:bgPr` element with noFill properties.

### `bgPr`

```python
bgPr: CT_BackgroundProperties | None = ZeroOrOne('p:bgPr', successors=())
```

### `bgRef`

```python
bgRef = ZeroOrOne('p:bgRef', successors=())
```

## `CT_BackgroundProperties`

Bases: `BaseOxmlElement`

`p:bgPr` element.

### `eg_fillProperties`

```python
eg_fillProperties = ZeroOrOneChoice((Choice('a:noFill'), Choice('a:solidFill'), Choice('a:gradFill'), Choice('a:blipFill'), Choice('a:pattFill'), Choice('a:grpFill')), successors=_tag_seq[6:])
```

## `CT_CommonSlideData`

Bases: `BaseOxmlElement`

`p:cSld` element.

### `bg`

```python
bg: CT_Background | None = ZeroOrOne('p:bg', successors=_tag_seq[1:])
```

### `get_or_add_bg`

```python
get_or_add_bg: Callable[[], CT_Background]
```

### `get_or_add_bgPr`

```python
get_or_add_bgPr() -> CT_BackgroundProperties
```

Return `p:bg/p:bgPr` grandchild.

If no such grandchild is present, any existing `p:bg` child is first removed and a new
default `p:bg` with noFill settings is added.

### `name`

```python
name: str = OptionalAttribute('name', XsdString, default='')
```

### `spTree`

```python
spTree: CT_GroupShape = OneAndOnlyOne('p:spTree')
```

## `CT_HeaderFooter`

Bases: `BaseOxmlElement`

`p:hf` element, header/footer placeholder visibility flags (paper-pptx addition).

All four attributes are optional booleans whose schema default is true (visible).

### `dt`

```python
dt: bool | None = OptionalAttribute('dt', XsdBoolean)
```

### `ftr`

```python
ftr: bool | None = OptionalAttribute('ftr', XsdBoolean)
```

### `hdr`

```python
hdr: bool | None = OptionalAttribute('hdr', XsdBoolean)
```

### `sldNum`

```python
sldNum: bool | None = OptionalAttribute('sldNum', XsdBoolean)
```

## `CT_NotesMaster`

Bases: `_BaseSlideElement`

`p:notesMaster` element, root of a notes master part.

### `cSld`

```python
cSld: CT_CommonSlideData = OneAndOnlyOne('p:cSld')
```

### `new_default`

```python
new_default() -> CT_NotesMaster
```

Return a new `p:notesMaster` element based on the built-in default template.

## `CT_NotesSlide`

Bases: `_BaseSlideElement`

`p:notes` element, root of a notes slide part.

### `cSld`

```python
cSld: CT_CommonSlideData = OneAndOnlyOne('p:cSld')
```

### `new`

```python
new() -> CT_NotesSlide
```

Return a new ``<p:notes>`` element based on the default template.

Note that the template does not include placeholders, which must be subsequently cloned
from the notes master.

## `CT_Slide`

Bases: `_BaseSlideElement`

`p:sld` element, root element of a slide part (XML document).

### `bg`

```python
bg
```

Return `p:bg` grandchild or None if not present.

### `cSld`

```python
cSld: CT_CommonSlideData = OneAndOnlyOne('p:cSld')
```

### `clrMapOvr`

```python
clrMapOvr = ZeroOrOne('p:clrMapOvr', successors=_tag_seq[2:])
```

### `get_or_add_childTnLst`

```python
get_or_add_childTnLst()
```

Return parent element for a new `p:video` child element.

The `p:video` element causes play controls to appear under a video
shape (pic shape containing video). There can be more than one video
shape on a slide, which causes the precondition to vary. It needs to
handle the case when there is no `p:sld/p:timing` element and when
that element already exists. If the case isn't simple, it just nukes
what's there and adds a fresh one. This could theoretically remove
desired existing timing information, but there isn't any evidence
available to me one way or the other, so I've taken the simple
approach.

### `new`

```python
new() -> CT_Slide
```

Return new `p:sld` element configured as base slide shape.

### `timing`

```python
timing = ZeroOrOne('p:timing', successors=_tag_seq[4:])
```

## `CT_SlideLayout`

Bases: `_BaseSlideElement`

`p:sldLayout` element, root of a slide layout part.

### `cSld`

```python
cSld: CT_CommonSlideData = OneAndOnlyOne('p:cSld')
```

### `get_or_add_hf`

```python
get_or_add_hf: Callable[[], CT_HeaderFooter]
```

### `hf`

```python
hf: CT_HeaderFooter | None = ZeroOrOne('p:hf', successors=_tag_seq[5:])
```

## `CT_SlideLayoutIdList`

Bases: `BaseOxmlElement`

`p:sldLayoutIdLst` element, child of `p:sldMaster`.

Contains references to the slide layouts that inherit from the slide master.

### `sldLayoutId`

```python
sldLayoutId = ZeroOrMore('p:sldLayoutId')
```

### `sldLayoutId_lst`

```python
sldLayoutId_lst: list[CT_SlideLayoutIdListEntry]
```

## `CT_SlideLayoutIdListEntry`

Bases: `BaseOxmlElement`

`p:sldLayoutId` element, child of `p:sldLayoutIdLst`.

Contains a reference to a slide layout.

### `id`

```python
id: int | None = OptionalAttribute('id', XsdUnsignedInt)
```

### `rId`

```python
rId: str = RequiredAttribute('r:id', XsdString)
```

## `CT_SlideMaster`

Bases: `_BaseSlideElement`

`p:sldMaster` element, root of a slide master part.

### `cSld`

```python
cSld: CT_CommonSlideData = OneAndOnlyOne('p:cSld')
```

### `get_or_add_hf`

```python
get_or_add_hf: Callable[[], CT_HeaderFooter]
```

### `get_or_add_sldLayoutIdLst`

```python
get_or_add_sldLayoutIdLst: Callable[[], CT_SlideLayoutIdList]
```

### `hf`

```python
hf: CT_HeaderFooter | None = ZeroOrOne('p:hf', successors=_tag_seq[6:])
```

### `sldLayoutIdLst`

```python
sldLayoutIdLst: CT_SlideLayoutIdList = ZeroOrOne('p:sldLayoutIdLst', successors=_tag_seq[3:])
```

### `txStyles`

```python
txStyles: CT_SlideMasterTextStyles | None = ZeroOrOne('p:txStyles', successors=_tag_seq[7:])
```

## `CT_SlideMasterTextStyles`

Bases: `BaseOxmlElement`

`p:txStyles` element, holding the master's title/body/other text list-styles.

Read-only access for the effective-style inheritance walk; each child is a
`CT_TextListStyle`.

### `bodyStyle`

```python
bodyStyle
```

`p:bodyStyle` child (a `CT_TextListStyle`), or `None` if not present.

### `otherStyle`

```python
otherStyle
```

`p:otherStyle` child (a `CT_TextListStyle`), or `None` if not present.

### `titleStyle`

```python
titleStyle
```

`p:titleStyle` child (a `CT_TextListStyle`), or `None` if not present.

## `CT_SlideTiming`

Bases: `BaseOxmlElement`

`p:timing` element, specifying animations and timed behaviors.

### `tnLst`

```python
tnLst = ZeroOrOne('p:tnLst', successors=_tag_seq[1:])
```

## `CT_TLMediaNodeVideo`

Bases: `BaseOxmlElement`

`p:video` element, specifying video media details.

### `cMediaNode`

```python
cMediaNode = OneAndOnlyOne('p:cMediaNode')
```

## `CT_TimeNodeList`

Bases: `BaseOxmlElement`

`p:tnLst` or `p:childTnList` element.

### `add_video`

```python
add_video(shape_id)
```

Add a new `p:video` child element for movie having *shape_id*.
