<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.text.paragraph`

Custom element classes related to paragraphs (CT_P).

## `CT_P`

Bases: `BaseOxmlElement`

`<w:p>` element, containing the properties and text for a paragraph.

### `add_p_before`

```python
add_p_before() -> CT_P
```

Return a new `<w:p>` element inserted directly prior to this one.

### `add_r`

```python
add_r: Callable[[], CT_R]
```

### `alignment`

```python
alignment: WD_PARAGRAPH_ALIGNMENT | None
```

The value of the `<w:jc>` grandchild element or `None` if not present.

### `clear_content`

```python
clear_content()
```

Remove all child elements, except the `<w:pPr>` element if present.

### `get_or_add_pPr`

```python
get_or_add_pPr: Callable[[], CT_PPr]
```

### `hyperlink`

```python
hyperlink = ZeroOrMore('w:hyperlink')
```

### `hyperlink_lst`

```python
hyperlink_lst: List[CT_Hyperlink]
```

### `inner_content_elements`

```python
inner_content_elements: List[CT_R | CT_Hyperlink]
```

Run and hyperlink children of the `w:p` element, in document order.

### `lastRenderedPageBreaks`

```python
lastRenderedPageBreaks: List[CT_LastRenderedPageBreak]
```

All `w:lastRenderedPageBreak` descendants of this paragraph.

Rendered page-breaks commonly occur in a run but can also occur in a run inside
a hyperlink. This returns both.

### `pPr`

```python
pPr: CT_PPr | None = ZeroOrOne('w:pPr')
```

### `r`

```python
r = ZeroOrMore('w:r')
```

### `r_lst`

```python
r_lst: List[CT_R]
```

### `set_sectPr`

```python
set_sectPr(sectPr: CT_SectPr)
```

Unconditionally replace or add `sectPr` as grandchild in correct sequence.

### `style`

```python
style: str | None
```

String contained in `w:val` attribute of `./w:pPr/w:pStyle` grandchild.

`None` if not present.

### `text`

```python
text
```

The textual content of this paragraph.

Inner-content child elements like `w:r` and `w:hyperlink` are translated to
their text equivalent.
