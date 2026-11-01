<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.enum.style`

Enumerations related to styles.

## `WD_BUILTIN_STYLE`

Bases: `BaseEnum`

Alias: **WD_STYLE**

Specifies a built-in Microsoft Word style.

Example::

    from docx import Document
    from docx.enum.style import WD_STYLE

    document = Document()
    styles = document.styles
    style = styles[WD_STYLE.BODY_TEXT]


MS API name: `WdBuiltinStyle`

http://msdn.microsoft.com/en-us/library/office/ff835210.aspx

### `BLOCK_QUOTATION`

```python
BLOCK_QUOTATION = (-85, 'Block Text.')
```

Block Text.

### `BODY_TEXT`

```python
BODY_TEXT = (-67, 'Body Text.')
```

Body Text.

### `BODY_TEXT_2`

```python
BODY_TEXT_2 = (-81, 'Body Text 2.')
```

Body Text 2.

### `BODY_TEXT_3`

```python
BODY_TEXT_3 = (-82, 'Body Text 3.')
```

Body Text 3.

### `BODY_TEXT_FIRST_INDENT`

```python
BODY_TEXT_FIRST_INDENT = (-78, 'Body Text First Indent.')
```

Body Text First Indent.

### `BODY_TEXT_FIRST_INDENT_2`

```python
BODY_TEXT_FIRST_INDENT_2 = (-79, 'Body Text First Indent 2.')
```

Body Text First Indent 2.

### `BODY_TEXT_INDENT`

```python
BODY_TEXT_INDENT = (-68, 'Body Text Indent.')
```

Body Text Indent.

### `BODY_TEXT_INDENT_2`

```python
BODY_TEXT_INDENT_2 = (-83, 'Body Text Indent 2.')
```

Body Text Indent 2.

### `BODY_TEXT_INDENT_3`

```python
BODY_TEXT_INDENT_3 = (-84, 'Body Text Indent 3.')
```

Body Text Indent 3.

### `BOOK_TITLE`

```python
BOOK_TITLE = (-265, 'Book Title.')
```

Book Title.

### `CAPTION`

```python
CAPTION = (-35, 'Caption.')
```

Caption.

### `CLOSING`

```python
CLOSING = (-64, 'Closing.')
```

Closing.

### `COMMENT_REFERENCE`

```python
COMMENT_REFERENCE = (-40, 'Comment Reference.')
```

Comment Reference.

### `COMMENT_TEXT`

```python
COMMENT_TEXT = (-31, 'Comment Text.')
```

Comment Text.

### `DATE`

```python
DATE = (-77, 'Date.')
```

Date.

### `DEFAULT_PARAGRAPH_FONT`

```python
DEFAULT_PARAGRAPH_FONT = (-66, 'Default Paragraph Font.')
```

Default Paragraph Font.

### `EMPHASIS`

```python
EMPHASIS = (-89, 'Emphasis.')
```

Emphasis.

### `ENDNOTE_REFERENCE`

```python
ENDNOTE_REFERENCE = (-43, 'Endnote Reference.')
```

Endnote Reference.

### `ENDNOTE_TEXT`

```python
ENDNOTE_TEXT = (-44, 'Endnote Text.')
```

Endnote Text.

### `ENVELOPE_ADDRESS`

```python
ENVELOPE_ADDRESS = (-37, 'Envelope Address.')
```

Envelope Address.

### `ENVELOPE_RETURN`

```python
ENVELOPE_RETURN = (-38, 'Envelope Return.')
```

Envelope Return.

### `FOOTER`

```python
FOOTER = (-33, 'Footer.')
```

Footer.

### `FOOTNOTE_REFERENCE`

```python
FOOTNOTE_REFERENCE = (-39, 'Footnote Reference.')
```

Footnote Reference.

### `FOOTNOTE_TEXT`

```python
FOOTNOTE_TEXT = (-30, 'Footnote Text.')
```

Footnote Text.

### `HEADER`

```python
HEADER = (-32, 'Header.')
```

Header.

### `HEADING_1`

```python
HEADING_1 = (-2, 'Heading 1.')
```

Heading 1.

### `HEADING_2`

```python
HEADING_2 = (-3, 'Heading 2.')
```

Heading 2.

### `HEADING_3`

```python
HEADING_3 = (-4, 'Heading 3.')
```

Heading 3.

### `HEADING_4`

```python
HEADING_4 = (-5, 'Heading 4.')
```

Heading 4.

### `HEADING_5`

```python
HEADING_5 = (-6, 'Heading 5.')
```

Heading 5.

### `HEADING_6`

```python
HEADING_6 = (-7, 'Heading 6.')
```

Heading 6.

### `HEADING_7`

```python
HEADING_7 = (-8, 'Heading 7.')
```

Heading 7.

### `HEADING_8`

```python
HEADING_8 = (-9, 'Heading 8.')
```

Heading 8.

### `HEADING_9`

```python
HEADING_9 = (-10, 'Heading 9.')
```

Heading 9.

### `HTML_ACRONYM`

```python
HTML_ACRONYM = (-96, 'HTML Acronym.')
```

HTML Acronym.

### `HTML_ADDRESS`

```python
HTML_ADDRESS = (-97, 'HTML Address.')
```

HTML Address.

### `HTML_CITE`

```python
HTML_CITE = (-98, 'HTML Cite.')
```

HTML Cite.

### `HTML_CODE`

```python
HTML_CODE = (-99, 'HTML Code.')
```

HTML Code.

### `HTML_DFN`

```python
HTML_DFN = (-100, 'HTML Definition.')
```

HTML Definition.

### `HTML_KBD`

```python
HTML_KBD = (-101, 'HTML Keyboard.')
```

HTML Keyboard.

### `HTML_NORMAL`

```python
HTML_NORMAL = (-95, 'Normal (Web).')
```

Normal (Web).

### `HTML_PRE`

```python
HTML_PRE = (-102, 'HTML Preformatted.')
```

HTML Preformatted.

### `HTML_SAMP`

```python
HTML_SAMP = (-103, 'HTML Sample.')
```

HTML Sample.

### `HTML_TT`

```python
HTML_TT = (-104, 'HTML Typewriter.')
```

HTML Typewriter.

### `HTML_VAR`

```python
HTML_VAR = (-105, 'HTML Variable.')
```

HTML Variable.

### `HYPERLINK`

```python
HYPERLINK = (-86, 'Hyperlink.')
```

Hyperlink.

### `HYPERLINK_FOLLOWED`

```python
HYPERLINK_FOLLOWED = (-87, 'Followed Hyperlink.')
```

Followed Hyperlink.

### `INDEX_1`

```python
INDEX_1 = (-11, 'Index 1.')
```

Index 1.

### `INDEX_2`

```python
INDEX_2 = (-12, 'Index 2.')
```

Index 2.

### `INDEX_3`

```python
INDEX_3 = (-13, 'Index 3.')
```

Index 3.

### `INDEX_4`

```python
INDEX_4 = (-14, 'Index 4.')
```

Index 4.

### `INDEX_5`

```python
INDEX_5 = (-15, 'Index 5.')
```

Index 5.

### `INDEX_6`

```python
INDEX_6 = (-16, 'Index 6.')
```

Index 6.

### `INDEX_7`

```python
INDEX_7 = (-17, 'Index 7.')
```

Index 7.

### `INDEX_8`

```python
INDEX_8 = (-18, 'Index 8.')
```

Index 8.

### `INDEX_9`

```python
INDEX_9 = (-19, 'Index 9.')
```

Index 9.

### `INDEX_HEADING`

```python
INDEX_HEADING = (-34, 'Index Heading')
```

Index Heading

### `INTENSE_EMPHASIS`

```python
INTENSE_EMPHASIS = (-262, 'Intense Emphasis.')
```

Intense Emphasis.

### `INTENSE_QUOTE`

```python
INTENSE_QUOTE = (-182, 'Intense Quote.')
```

Intense Quote.

### `INTENSE_REFERENCE`

```python
INTENSE_REFERENCE = (-264, 'Intense Reference.')
```

Intense Reference.

### `LINE_NUMBER`

```python
LINE_NUMBER = (-41, 'Line Number.')
```

Line Number.

### `LIST`

```python
LIST = (-48, 'List.')
```

List.

### `LIST_2`

```python
LIST_2 = (-51, 'List 2.')
```

List 2.

### `LIST_3`

```python
LIST_3 = (-52, 'List 3.')
```

List 3.

### `LIST_4`

```python
LIST_4 = (-53, 'List 4.')
```

List 4.

### `LIST_5`

```python
LIST_5 = (-54, 'List 5.')
```

List 5.

### `LIST_BULLET`

```python
LIST_BULLET = (-49, 'List Bullet.')
```

List Bullet.

### `LIST_BULLET_2`

```python
LIST_BULLET_2 = (-55, 'List Bullet 2.')
```

List Bullet 2.

### `LIST_BULLET_3`

```python
LIST_BULLET_3 = (-56, 'List Bullet 3.')
```

List Bullet 3.

### `LIST_BULLET_4`

```python
LIST_BULLET_4 = (-57, 'List Bullet 4.')
```

List Bullet 4.

### `LIST_BULLET_5`

```python
LIST_BULLET_5 = (-58, 'List Bullet 5.')
```

List Bullet 5.

### `LIST_CONTINUE`

```python
LIST_CONTINUE = (-69, 'List Continue.')
```

List Continue.

### `LIST_CONTINUE_2`

```python
LIST_CONTINUE_2 = (-70, 'List Continue 2.')
```

List Continue 2.

### `LIST_CONTINUE_3`

```python
LIST_CONTINUE_3 = (-71, 'List Continue 3.')
```

List Continue 3.

### `LIST_CONTINUE_4`

```python
LIST_CONTINUE_4 = (-72, 'List Continue 4.')
```

List Continue 4.

### `LIST_CONTINUE_5`

```python
LIST_CONTINUE_5 = (-73, 'List Continue 5.')
```

List Continue 5.

### `LIST_NUMBER`

```python
LIST_NUMBER = (-50, 'List Number.')
```

List Number.

### `LIST_NUMBER_2`

```python
LIST_NUMBER_2 = (-59, 'List Number 2.')
```

List Number 2.

### `LIST_NUMBER_3`

```python
LIST_NUMBER_3 = (-60, 'List Number 3.')
```

List Number 3.

### `LIST_NUMBER_4`

```python
LIST_NUMBER_4 = (-61, 'List Number 4.')
```

List Number 4.

### `LIST_NUMBER_5`

```python
LIST_NUMBER_5 = (-62, 'List Number 5.')
```

List Number 5.

### `LIST_PARAGRAPH`

```python
LIST_PARAGRAPH = (-180, 'List Paragraph.')
```

List Paragraph.

### `MACRO_TEXT`

```python
MACRO_TEXT = (-46, 'Macro Text.')
```

Macro Text.

### `MESSAGE_HEADER`

```python
MESSAGE_HEADER = (-74, 'Message Header.')
```

Message Header.

### `NAV_PANE`

```python
NAV_PANE = (-90, 'Document Map.')
```

Document Map.

### `NORMAL`

```python
NORMAL = (-1, 'Normal.')
```

Normal.

### `NORMAL_INDENT`

```python
NORMAL_INDENT = (-29, 'Normal Indent.')
```

Normal Indent.

### `NORMAL_OBJECT`

```python
NORMAL_OBJECT = (-158, 'Normal (applied to an object).')
```

Normal (applied to an object).

### `NORMAL_TABLE`

```python
NORMAL_TABLE = (-106, 'Normal (applied within a table).')
```

Normal (applied within a table).

### `NOTE_HEADING`

```python
NOTE_HEADING = (-80, 'Note Heading.')
```

Note Heading.

### `PAGE_NUMBER`

```python
PAGE_NUMBER = (-42, 'Page Number.')
```

Page Number.

### `PLAIN_TEXT`

```python
PLAIN_TEXT = (-91, 'Plain Text.')
```

Plain Text.

### `QUOTE`

```python
QUOTE = (-181, 'Quote.')
```

Quote.

### `SALUTATION`

```python
SALUTATION = (-76, 'Salutation.')
```

Salutation.

### `SIGNATURE`

```python
SIGNATURE = (-65, 'Signature.')
```

Signature.

### `STRONG`

```python
STRONG = (-88, 'Strong.')
```

Strong.

### `SUBTITLE`

```python
SUBTITLE = (-75, 'Subtitle.')
```

Subtitle.

### `SUBTLE_EMPHASIS`

```python
SUBTLE_EMPHASIS = (-261, 'Subtle Emphasis.')
```

Subtle Emphasis.

### `SUBTLE_REFERENCE`

```python
SUBTLE_REFERENCE = (-263, 'Subtle Reference.')
```

Subtle Reference.

### `TABLE_COLORFUL_GRID`

```python
TABLE_COLORFUL_GRID = (-172, 'Colorful Grid.')
```

Colorful Grid.

### `TABLE_COLORFUL_LIST`

```python
TABLE_COLORFUL_LIST = (-171, 'Colorful List.')
```

Colorful List.

### `TABLE_COLORFUL_SHADING`

```python
TABLE_COLORFUL_SHADING = (-170, 'Colorful Shading.')
```

Colorful Shading.

### `TABLE_DARK_LIST`

```python
TABLE_DARK_LIST = (-169, 'Dark List.')
```

Dark List.

### `TABLE_LIGHT_GRID`

```python
TABLE_LIGHT_GRID = (-161, 'Light Grid.')
```

Light Grid.

### `TABLE_LIGHT_GRID_ACCENT_1`

```python
TABLE_LIGHT_GRID_ACCENT_1 = (-175, 'Light Grid Accent 1.')
```

Light Grid Accent 1.

### `TABLE_LIGHT_LIST`

```python
TABLE_LIGHT_LIST = (-160, 'Light List.')
```

Light List.

### `TABLE_LIGHT_LIST_ACCENT_1`

```python
TABLE_LIGHT_LIST_ACCENT_1 = (-174, 'Light List Accent 1.')
```

Light List Accent 1.

### `TABLE_LIGHT_SHADING`

```python
TABLE_LIGHT_SHADING = (-159, 'Light Shading.')
```

Light Shading.

### `TABLE_LIGHT_SHADING_ACCENT_1`

```python
TABLE_LIGHT_SHADING_ACCENT_1 = (-173, 'Light Shading Accent 1.')
```

Light Shading Accent 1.

### `TABLE_MEDIUM_GRID_1`

```python
TABLE_MEDIUM_GRID_1 = (-166, 'Medium Grid 1.')
```

Medium Grid 1.

### `TABLE_MEDIUM_GRID_2`

```python
TABLE_MEDIUM_GRID_2 = (-167, 'Medium Grid 2.')
```

Medium Grid 2.

### `TABLE_MEDIUM_GRID_3`

```python
TABLE_MEDIUM_GRID_3 = (-168, 'Medium Grid 3.')
```

Medium Grid 3.

### `TABLE_MEDIUM_LIST_1`

```python
TABLE_MEDIUM_LIST_1 = (-164, 'Medium List 1.')
```

Medium List 1.

### `TABLE_MEDIUM_LIST_1_ACCENT_1`

```python
TABLE_MEDIUM_LIST_1_ACCENT_1 = (-178, 'Medium List 1 Accent 1.')
```

Medium List 1 Accent 1.

### `TABLE_MEDIUM_LIST_2`

```python
TABLE_MEDIUM_LIST_2 = (-165, 'Medium List 2.')
```

Medium List 2.

### `TABLE_MEDIUM_SHADING_1`

```python
TABLE_MEDIUM_SHADING_1 = (-162, 'Medium Shading 1.')
```

Medium Shading 1.

### `TABLE_MEDIUM_SHADING_1_ACCENT_1`

```python
TABLE_MEDIUM_SHADING_1_ACCENT_1 = (-176, 'Medium Shading 1 Accent 1.')
```

Medium Shading 1 Accent 1.

### `TABLE_MEDIUM_SHADING_2`

```python
TABLE_MEDIUM_SHADING_2 = (-163, 'Medium Shading 2.')
```

Medium Shading 2.

### `TABLE_MEDIUM_SHADING_2_ACCENT_1`

```python
TABLE_MEDIUM_SHADING_2_ACCENT_1 = (-177, 'Medium Shading 2 Accent 1.')
```

Medium Shading 2 Accent 1.

### `TABLE_OF_AUTHORITIES`

```python
TABLE_OF_AUTHORITIES = (-45, 'Table of Authorities.')
```

Table of Authorities.

### `TABLE_OF_FIGURES`

```python
TABLE_OF_FIGURES = (-36, 'Table of Figures.')
```

Table of Figures.

### `TITLE`

```python
TITLE = (-63, 'Title.')
```

Title.

### `TOAHEADING`

```python
TOAHEADING = (-47, 'TOA Heading.')
```

TOA Heading.

### `TOC_1`

```python
TOC_1 = (-20, 'TOC 1.')
```

TOC 1.

### `TOC_2`

```python
TOC_2 = (-21, 'TOC 2.')
```

TOC 2.

### `TOC_3`

```python
TOC_3 = (-22, 'TOC 3.')
```

TOC 3.

### `TOC_4`

```python
TOC_4 = (-23, 'TOC 4.')
```

TOC 4.

### `TOC_5`

```python
TOC_5 = (-24, 'TOC 5.')
```

TOC 5.

### `TOC_6`

```python
TOC_6 = (-25, 'TOC 6.')
```

TOC 6.

### `TOC_7`

```python
TOC_7 = (-26, 'TOC 7.')
```

TOC 7.

### `TOC_8`

```python
TOC_8 = (-27, 'TOC 8.')
```

TOC 8.

### `TOC_9`

```python
TOC_9 = (-28, 'TOC 9.')
```

TOC 9.

## `WD_STYLE`

```python
WD_STYLE = WD_BUILTIN_STYLE
```

## `WD_STYLE_TYPE`

Bases: `BaseXmlEnum`

Specifies one of the four style types: paragraph, character, list, or table.

Example::

    from docx import Document
    from docx.enum.style import WD_STYLE_TYPE

    styles = Document().styles
    assert styles[0].type == WD_STYLE_TYPE.PARAGRAPH

MS API name: `WdStyleType`

http://msdn.microsoft.com/en-us/library/office/ff196870.aspx

### `CHARACTER`

```python
CHARACTER = (2, 'character', 'Character style.')
```

Character style.

### `LIST`

```python
LIST = (4, 'numbering', 'List style.')
```

List style.

### `PARAGRAPH`

```python
PARAGRAPH = (1, 'paragraph', 'Paragraph style.')
```

Paragraph style.

### `TABLE`

```python
TABLE = (3, 'table', 'Table style.')
```

Table style.
