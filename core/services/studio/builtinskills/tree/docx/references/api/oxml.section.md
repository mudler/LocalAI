<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.section`

Section-related custom element classes.

## `BlockElement`

```python
BlockElement: TypeAlias = 'CT_P | CT_Tbl'
```

## `CT_HdrFtr`

Bases: `BaseOxmlElement`

`w:hdr` and `w:ftr`, the root element for header and footer part respectively.

### `add_p`

```python
add_p: Callable[[], CT_P]
```

### `inner_content_elements`

```python
inner_content_elements: List[CT_P | CT_Tbl]
```

Generate all `w:p` and `w:tbl` elements in this header or footer.

Elements appear in document order. Elements shaded by nesting in a `w:ins` or
other "wrapper" element will not be included.

### `p`

```python
p = ZeroOrMore('w:p', successors=())
```

### `p_lst`

```python
p_lst: List[CT_P]
```

### `tbl`

```python
tbl = ZeroOrMore('w:tbl', successors=())
```

### `tbl_lst`

```python
tbl_lst: List[CT_Tbl]
```

## `CT_HdrFtrRef`

Bases: `BaseOxmlElement`

`w:headerReference` and `w:footerReference` elements.

### `rId`

```python
rId: str = RequiredAttribute('r:id', XsdString)
```

### `type_`

```python
type_: WD_HEADER_FOOTER = RequiredAttribute('w:type', WD_HEADER_FOOTER)
```

## `CT_PageMar`

Bases: `BaseOxmlElement`

``<w:pgMar>`` element, defining page margins.

### `bottom`

```python
bottom: Length | None = OptionalAttribute('w:bottom', ST_SignedTwipsMeasure)
```

### `footer`

```python
footer: Length | None = OptionalAttribute('w:footer', ST_TwipsMeasure)
```

### `gutter`

```python
gutter: Length | None = OptionalAttribute('w:gutter', ST_TwipsMeasure)
```

### `header`

```python
header: Length | None = OptionalAttribute('w:header', ST_TwipsMeasure)
```

### `left`

```python
left: Length | None = OptionalAttribute('w:left', ST_TwipsMeasure)
```

### `right`

```python
right: Length | None = OptionalAttribute('w:right', ST_TwipsMeasure)
```

### `top`

```python
top: Length | None = OptionalAttribute('w:top', ST_SignedTwipsMeasure)
```

## `CT_PageSz`

Bases: `BaseOxmlElement`

``<w:pgSz>`` element, defining page dimensions and orientation.

### `h`

```python
h: Length | None = OptionalAttribute('w:h', ST_TwipsMeasure)
```

### `orient`

```python
orient: WD_ORIENTATION = OptionalAttribute('w:orient', WD_ORIENTATION, default=WD_ORIENTATION.PORTRAIT)
```

### `w`

```python
w: Length | None = OptionalAttribute('w:w', ST_TwipsMeasure)
```

## `CT_SectPr`

Bases: `BaseOxmlElement`

`w:sectPr` element, the container element for section properties.

### `add_footerReference`

```python
add_footerReference(type_: WD_HEADER_FOOTER, rId: str) -> CT_HdrFtrRef
```

Return newly added CT_HdrFtrRef element of `type_` with `rId`.

The element tag is `w:footerReference`.

### `add_headerReference`

```python
add_headerReference(type_: WD_HEADER_FOOTER, rId: str) -> CT_HdrFtrRef
```

Return newly added CT_HdrFtrRef element of `type_` with `rId`.

The element tag is `w:headerReference`.

### `bottom_margin`

```python
bottom_margin: Length | None
```

Value of the `w:bottom` attr of `<w:pgMar>` child element, as `Length`.

`None` when either the element or the attribute is not present.

### `clone`

```python
clone() -> CT_SectPr
```

Return an exact duplicate of this ``<w:sectPr>`` element tree suitable for
use in adding a section break.

All rsid* attributes are removed from the root ``<w:sectPr>`` element.

### `footer`

```python
footer: Length | None
```

Distance from bottom edge of page to bottom edge of the footer.

This is the value of the `w:footer` attribute in the `w:pgMar` child element,
as a `Length` object, or `None` if either the element or the attribute is not
present.

### `footerReference`

```python
footerReference = ZeroOrMore('w:footerReference', successors=_tag_seq)
```

### `get_footerReference`

```python
get_footerReference(type_: WD_HEADER_FOOTER) -> CT_HdrFtrRef | None
```

Return footerReference element of `type_` or None if not present.

### `get_headerReference`

```python
get_headerReference(type_: WD_HEADER_FOOTER) -> CT_HdrFtrRef | None
```

Return headerReference element of `type_` or None if not present.

### `get_or_add_pgMar`

```python
get_or_add_pgMar: Callable[[], CT_PageMar]
```

### `get_or_add_pgSz`

```python
get_or_add_pgSz: Callable[[], CT_PageSz]
```

### `get_or_add_titlePg`

```python
get_or_add_titlePg: Callable[[], CT_OnOff]
```

### `get_or_add_type`

```python
get_or_add_type: Callable[[], CT_SectType]
```

### `gutter`

```python
gutter: Length | None
```

The value of the ``w:gutter`` attribute in the ``<w:pgMar>`` child element,
as a `Length` object, or `None` if either the element or the attribute is not
present.

### `header`

```python
header: Length | None
```

Distance from top edge of page to top edge of header.

This value comes from the `w:header` attribute on the `w:pgMar` child element.
`None` if either the element or the attribute is not present.

### `headerReference`

```python
headerReference = ZeroOrMore('w:headerReference', successors=_tag_seq)
```

### `iter_inner_content`

```python
iter_inner_content() -> Iterator[CT_P | CT_Tbl]
```

Generate all `w:p` and `w:tbl` elements in this section.

Elements appear in document order. Elements shaded by nesting in a `w:ins` or
other "wrapper" element will not be included.

### `left_margin`

```python
left_margin: Length | None
```

The value of the ``w:left`` attribute in the ``<w:pgMar>`` child element, as
a `Length` object, or `None` if either the element or the attribute is not
present.

### `orientation`

```python
orientation: WD_ORIENTATION
```

`WD_ORIENTATION` member indicating page-orientation for this section.

This is the value of the `orient` attribute on the `w:pgSz` child, or
`WD_ORIENTATION.PORTRAIT` if not present.

### `page_height`

```python
page_height: Length | None
```

Value in EMU of the `h` attribute of the `w:pgSz` child element.

`None` if not present.

### `page_width`

```python
page_width: Length | None
```

Value in EMU of the ``w`` attribute of the ``<w:pgSz>`` child element.

`None` if not present.

### `pgMar`

```python
pgMar: CT_PageMar | None = ZeroOrOne('w:pgMar', successors=_tag_seq[5:])
```

### `pgSz`

```python
pgSz: CT_PageSz | None = ZeroOrOne('w:pgSz', successors=_tag_seq[4:])
```

### `preceding_sectPr`

```python
preceding_sectPr: CT_SectPr | None
```

SectPr immediately preceding this one or None if this is the first.

### `remove_footerReference`

```python
remove_footerReference(type_: WD_HEADER_FOOTER) -> str
```

Return rId of w:footerReference child of `type_` after removing it.

### `remove_headerReference`

```python
remove_headerReference(type_: WD_HEADER_FOOTER)
```

Return rId of w:headerReference child of `type_` after removing it.

### `right_margin`

```python
right_margin: Length | None
```

The value of the ``w:right`` attribute in the ``<w:pgMar>`` child element, as
a `Length` object, or `None` if either the element or the attribute is not
present.

### `start_type`

```python
start_type: WD_SECTION_START
```

The member of the ``WD_SECTION_START`` enumeration corresponding to the value
of the ``val`` attribute of the ``<w:type>`` child element, or
``WD_SECTION_START.NEW_PAGE`` if not present.

### `titlePg`

```python
titlePg: CT_OnOff | None = ZeroOrOne('w:titlePg', successors=_tag_seq[14:])
```

### `titlePg_val`

```python
titlePg_val: bool
```

Value of `w:titlePg/@val` or `False` if `./w:titlePg` is not present.

### `top_margin`

```python
top_margin: Length | None
```

The value of the ``w:top`` attribute in the ``<w:pgMar>`` child element, as a
`Length` object, or `None` if either the element or the attribute is not
present.

### `type`

```python
type: CT_SectType | None = ZeroOrOne('w:type', successors=_tag_seq[3:])
```

## `CT_SectType`

Bases: `BaseOxmlElement`

``<w:sectType>`` element, defining the section start type.

### `val`

```python
val: WD_SECTION_START | None = OptionalAttribute('w:val', WD_SECTION_START)
```
