<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.presentation`

Custom element classes for presentation-related XML elements.

## `CT_NotesMasterIdList`

Bases: `BaseOxmlElement`

`p:notesMasterIdLst` element, child of `p:presentation` (paper-pptx addition).

Holds the reference to the presentation's notes master (at most one per schema).

### `get_or_add_notesMasterId`

```python
get_or_add_notesMasterId: 'Callable[[], CT_NotesMasterIdListEntry]'
```

### `notesMasterId`

```python
notesMasterId: 'CT_NotesMasterIdListEntry | None' = ZeroOrOne('p:notesMasterId')
```

## `CT_NotesMasterIdListEntry`

Bases: `BaseOxmlElement`

`p:notesMasterId` element - the reference to the notes master (paper-pptx addition).

### `rId`

```python
rId: str = RequiredAttribute('r:id', XsdString)
```

## `CT_Presentation`

Bases: `BaseOxmlElement`

`p:presentation` element, root of the Presentation part stored as `/ppt/presentation.xml`.

### `get_or_add_notesMasterIdLst`

```python
get_or_add_notesMasterIdLst: 'Callable[[], CT_NotesMasterIdList]'
```

### `get_or_add_sldIdLst`

```python
get_or_add_sldIdLst: Callable[[], CT_SlideIdList]
```

### `get_or_add_sldMasterIdLst`

```python
get_or_add_sldMasterIdLst: Callable[[], CT_SlideMasterIdList]
```

### `get_or_add_sldSz`

```python
get_or_add_sldSz: Callable[[], CT_SlideSize]
```

### `notesMasterIdLst`

```python
notesMasterIdLst: 'CT_NotesMasterIdList | None' = ZeroOrOne('p:notesMasterIdLst', successors=('p:handoutMasterIdLst', 'p:sldIdLst', 'p:sldSz', 'p:notesSz'))
```

### `sldIdLst`

```python
sldIdLst: CT_SlideIdList | None = ZeroOrOne('p:sldIdLst', successors=('p:sldSz', 'p:notesSz'))
```

### `sldMasterIdLst`

```python
sldMasterIdLst: CT_SlideMasterIdList | None = ZeroOrOne('p:sldMasterIdLst', successors=('p:notesMasterIdLst', 'p:handoutMasterIdLst', 'p:sldIdLst', 'p:sldSz', 'p:notesSz'))
```

### `sldSz`

```python
sldSz: CT_SlideSize | None = ZeroOrOne('p:sldSz', successors=('p:notesSz',))
```

## `CT_SlideId`

Bases: `BaseOxmlElement`

`p:sldId` element.

Direct child of `p:sldIdLst` that contains an `rId` reference to a slide in the presentation.

### `id`

```python
id: int = RequiredAttribute('id', ST_SlideId)
```

### `rId`

```python
rId: str = RequiredAttribute('r:id', XsdString)
```

## `CT_SlideIdList`

Bases: `BaseOxmlElement`

`p:sldIdLst` element.

Direct child of <p:presentation> that contains a list of the slide parts in the presentation.

### `add_sldId`

```python
add_sldId(rId: str) -> CT_SlideId
```

Create and return a reference to a new `p:sldId` child element.

The new `p:sldId` element has its r:id attribute set to `rId`.

### `sldId`

```python
sldId = ZeroOrMore('p:sldId')
```

### `sldId_lst`

```python
sldId_lst: list[CT_SlideId]
```

## `CT_SlideMasterIdList`

Bases: `BaseOxmlElement`

`p:sldMasterIdLst` element.

Child of `p:presentation` containing references to the slide masters that belong to the
presentation.

### `sldMasterId`

```python
sldMasterId = ZeroOrMore('p:sldMasterId')
```

### `sldMasterId_lst`

```python
sldMasterId_lst: list[CT_SlideMasterIdListEntry]
```

## `CT_SlideMasterIdListEntry`

Bases: `BaseOxmlElement`

``<p:sldMasterId>`` element, child of ``<p:sldMasterIdLst>`` containing
a reference to a slide master.

### `id`

```python
id: int | None = OptionalAttribute('id', XsdUnsignedInt)
```

### `rId`

```python
rId: str = RequiredAttribute('r:id', XsdString)
```

## `CT_SlideSize`

Bases: `BaseOxmlElement`

`p:sldSz` element.

Direct child of <p:presentation> that contains the width and height of slides in the
presentation.

### `cx`

```python
cx: Length = RequiredAttribute('cx', ST_SlideSizeCoordinate)
```

### `cy`

```python
cy: Length = RequiredAttribute('cy', ST_SlideSizeCoordinate)
```
