<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.document`

Custom element classes that correspond to the document part, e.g. <w:document>.

## `CT_Body`

Bases: `BaseOxmlElement`

`w:body`, the container element for the main document story in `document.xml`.

### `add_p`

```python
add_p: Callable[[], CT_P]
```

### `add_section_break`

```python
add_section_break() -> CT_SectPr
```

Return `w:sectPr` element for new section added at end of document.

The last `w:sectPr` becomes the second-to-last, with the new `w:sectPr` being an
exact clone of the previous one, except that all header and footer references
are removed (and are therefore now "inherited" from the prior section).

A copy of the previously-last `w:sectPr` will now appear in a new `w:p` at the
end of the document. The returned `w:sectPr` is the sentinel `w:sectPr` for the
document (and as implemented, `is` the prior sentinel `w:sectPr` with headers
and footers removed).

### `clear_content`

```python
clear_content()
```

Remove all content child elements from this <w:body> element.

Leave the <w:sectPr> element if it is present.

### `get_or_add_sectPr`

```python
get_or_add_sectPr: Callable[[], CT_SectPr]
```

### `inner_content_elements`

```python
inner_content_elements: List[CT_P | CT_Tbl]
```

Generate all `w:p` and `w:tbl` elements in this document-body.

Elements appear in document order. Elements shaded by nesting in a `w:ins` or
other "wrapper" element will not be included.

### `p`

```python
p = ZeroOrMore('w:p', successors=('w:sectPr',))
```

### `p_lst`

```python
p_lst: List[CT_P]
```

### `sectPr`

```python
sectPr: CT_SectPr | None = ZeroOrOne('w:sectPr', successors=())
```

### `tbl`

```python
tbl = ZeroOrMore('w:tbl', successors=('w:sectPr',))
```

### `tbl_lst`

```python
tbl_lst: List[CT_Tbl]
```

## `CT_Document`

Bases: `BaseOxmlElement`

``<w:document>`` element, the root element of a document.xml file.

### `body`

```python
body: CT_Body = ZeroOrOne('w:body')
```

### `sectPr_lst`

```python
sectPr_lst: List[CT_SectPr]
```

All `w:sectPr` elements directly accessible from document element.

Note this does not include a `sectPr` child in a paragraphs wrapped in
revision marks or other intervening layer, perhaps `w:sdt` or customXml
elements.

`w:sectPr` elements appear in document order. The last one is always
`w:body/w:sectPr`, all preceding are `w:p/w:pPr/w:sectPr`.
