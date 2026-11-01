<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.enum.section`

Enumerations related to the main document in WordprocessingML files.

## `WD_HEADER_FOOTER`

```python
WD_HEADER_FOOTER = WD_HEADER_FOOTER_INDEX
```

## `WD_HEADER_FOOTER_INDEX`

Bases: `BaseXmlEnum`

Alias: **WD_HEADER_FOOTER**

Specifies one of the three possible header/footer definitions for a section.

For internal use only; not part of the python-docx API.

MS API name: `WdHeaderFooterIndex`
URL: https://docs.microsoft.com/en-us/office/vba/api/word.wdheaderfooterindex

### `EVEN_PAGE`

```python
EVEN_PAGE = (3, 'even', 'Header for even pages of recto/verso section.')
```

Header for even pages of recto/verso section.

### `FIRST_PAGE`

```python
FIRST_PAGE = (2, 'first', 'Header for first page of section.')
```

Header for first page of section.

### `PRIMARY`

```python
PRIMARY = (1, 'default', 'Header for odd pages or all if no even header.')
```

Header for odd pages or all if no even header.

## `WD_ORIENT`

```python
WD_ORIENT = WD_ORIENTATION
```

## `WD_ORIENTATION`

Bases: `BaseXmlEnum`

Alias: **WD_ORIENT**

Specifies the page layout orientation.

Example::

    from docx.enum.section import WD_ORIENT

    section = document.sections[-1] section.orientation = WD_ORIENT.LANDSCAPE

MS API name: `WdOrientation`
MS API URL: http://msdn.microsoft.com/en-us/library/office/ff837902.aspx

### `LANDSCAPE`

```python
LANDSCAPE = (1, 'landscape', 'Landscape orientation.')
```

Landscape orientation.

### `PORTRAIT`

```python
PORTRAIT = (0, 'portrait', 'Portrait orientation.')
```

Portrait orientation.

## `WD_SECTION`

```python
WD_SECTION = WD_SECTION_START
```

## `WD_SECTION_START`

Bases: `BaseXmlEnum`

Alias: **WD_SECTION**

Specifies the start type of a section break.

Example::

    from docx.enum.section import WD_SECTION

    section = document.sections[0] section.start_type = WD_SECTION.NEW_PAGE

MS API name: `WdSectionStart`
MS API URL: http://msdn.microsoft.com/en-us/library/office/ff840975.aspx

### `CONTINUOUS`

```python
CONTINUOUS = (0, 'continuous', 'Continuous section break.')
```

Continuous section break.

### `EVEN_PAGE`

```python
EVEN_PAGE = (3, 'evenPage', 'Even pages section break.')
```

Even pages section break.

### `NEW_COLUMN`

```python
NEW_COLUMN = (1, 'nextColumn', 'New column section break.')
```

New column section break.

### `NEW_PAGE`

```python
NEW_PAGE = (2, 'nextPage', 'New page section break.')
```

New page section break.

### `ODD_PAGE`

```python
ODD_PAGE = (4, 'oddPage', 'Section begins on next odd page.')
```

Section begins on next odd page.
