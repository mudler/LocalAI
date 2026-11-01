<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.section`

The `Section` object and related proxy classes.

## `Section`

```python
Section(sectPr: CT_SectPr, document_part: DocumentPart)
```

Document section, providing access to section and page setup settings.

Also provides access to headers and footers.

### `bottom_margin`

```python
bottom_margin: Length | None
```

Read/write. Bottom margin for pages in this section, in EMU.

`None` when no bottom margin has been specified. Assigning `None` removes any
bottom-margin setting.

### `different_first_page_header_footer`

```python
different_first_page_header_footer: bool
```

True if this section displays a distinct first-page header and footer.

Read/write. The definition of the first-page header and footer are accessed
using `.first_page_header` and `.first_page_footer` respectively.

### `even_page_footer`

```python
even_page_footer: _Footer
```

`_Footer` object defining footer content for even pages.

The content of this footer definition is ignored unless the document setting
`odd_and_even_pages_header_footer` is set True.

### `even_page_header`

```python
even_page_header: _Header
```

`_Header` object defining header content for even pages.

The content of this header definition is ignored unless the document setting
`odd_and_even_pages_header_footer` is set True.

### `first_page_footer`

```python
first_page_footer: _Footer
```

`_Footer` object defining footer content for the first page of this section.

The content of this footer definition is ignored unless the property
`.different_first_page_header_footer` is set True.

### `first_page_header`

```python
first_page_header: _Header
```

`_Header` object defining header content for the first page of this section.

The content of this header definition is ignored unless the property
`.different_first_page_header_footer` is set True.

### `footer`

```python
footer() -> _Footer
```

`_Footer` object representing default page footer for this section.

The default footer is used for odd-numbered pages when separate odd/even footers
are enabled. It is used for both odd and even-numbered pages otherwise.

### `footer_distance`

```python
footer_distance: Length | None
```

Distance from bottom edge of page to bottom edge of the footer.

Read/write. `None` if no setting is present in the XML.

### `gutter`

```python
gutter: Length | None
```

`Length` object representing page gutter size in English Metric Units.

Read/write. The page gutter is extra spacing added to the `inner` margin to
ensure even margins after page binding. Generally only used in book-bound
documents with double-sided and facing pages.

This setting applies to all pages in this section.

### `header`

```python
header() -> _Header
```

`_Header` object representing default page header for this section.

The default header is used for odd-numbered pages when separate odd/even headers
are enabled. It is used for both odd and even-numbered pages otherwise.

### `header_distance`

```python
header_distance: Length | None
```

Distance from top edge of page to top edge of header.

Read/write. `None` if no setting is present in the XML. Assigning `None` causes
default value to be used.

### `iter_inner_content`

```python
iter_inner_content() -> Iterator[Paragraph | Table]
```

Generate each Paragraph or Table object in this `section`.

Items appear in document order.

### `left_margin`

```python
left_margin: Length | None
```

`Length` object representing the left margin for all pages in this section in
English Metric Units.

### `orientation`

```python
orientation: WD_ORIENTATION
```

`WdOrientation` member specifying page orientation for this section.

One of ``WD_ORIENT.PORTRAIT`` or ``WD_ORIENT.LANDSCAPE``.

### `page_height`

```python
page_height: Length | None
```

Total page height used for this section.

This value is inclusive of all edge spacing values such as margins.

Page orientation is taken into account, so for example, its expected value
would be ``Inches(8.5)`` for letter-sized paper when orientation is landscape.

### `page_width`

```python
page_width: Length | None
```

Total page width used for this section.

This value is like "paper size" and includes all edge spacing values such as
margins.

Page orientation is taken into account, so for example, its expected value
would be ``Inches(11)`` for letter-sized paper when orientation is landscape.

### `part`

```python
part: StoryPart
```

### `right_margin`

```python
right_margin: Length | None
```

`Length` object representing the right margin for all pages in this section
in English Metric Units.

### `start_type`

```python
start_type: WD_SECTION_START
```

Type of page-break (if any) inserted at the start of this section.

For exmple, ``WD_SECTION_START.ODD_PAGE`` if the section should begin on the
next odd page, possibly inserting two page-breaks instead of one.

### `top_margin`

```python
top_margin: Length | None
```

`Length` object representing the top margin for all pages in this section in
English Metric Units.

## `Sections`

```python
Sections(document_elm: CT_Document, document_part: DocumentPart)
```

Bases: `Sequence[Section]`

Sequence of `Section` objects corresponding to the sections in the document.

Supports ``len()``, iteration, and indexed access.
