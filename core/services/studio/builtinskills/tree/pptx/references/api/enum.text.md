<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.enum.text`

Enumerations used by text and related objects.

## `MSO_ANCHOR`

```python
MSO_ANCHOR = MSO_VERTICAL_ANCHOR
```

## `MSO_AUTO_SIZE`

Bases: `BaseEnum`

Determines the type of automatic sizing allowed.

The following names can be used to specify the automatic sizing behavior used to fit a shape's
text within the shape bounding box, for example::

    from pptx.enum.text import MSO_AUTO_SIZE

    shape.text_frame.auto_size = MSO_AUTO_SIZE.TEXT_TO_FIT_SHAPE

The word-wrap setting of the text frame interacts with the auto-size setting to determine the
specific auto-sizing behavior.

Note that `TextFrame.auto_size` can also be set to `None`, which removes the auto size setting
altogether. This causes the setting to be inherited, either from the layout placeholder, in the
case of a placeholder shape, or from the theme.

MS API Name: `MsoAutoSize`

http://msdn.microsoft.com/en-us/library/office/ff865367(v=office.15).aspx

### `MIXED`

```python
MIXED = (-2, 'Return value only; indicates a combination of automatic sizing schemes are used.')
```

Return value only; indicates a combination of automatic sizing schemes are used.

### `NONE`

```python
NONE = (0, 'No automatic sizing of the shape or text will be done.\n\nText can freely extend beyond the horizontal and vertical edges of the shape bounding box.')
```

No automatic sizing of the shape or text will be done.

Text can freely extend beyond the horizontal and vertical edges of the shape bounding box.

### `SHAPE_TO_FIT_TEXT`

```python
SHAPE_TO_FIT_TEXT = (1, 'The shape height and possibly width are adjusted to fit the text.\n\nNote this setting interacts with the TextFrame.word_wrap property setting. If word wrap is turned on, only the height of the shape will be adjusted; soft line breaks will be used to fit the text horizontally.')
```

The shape height and possibly width are adjusted to fit the text.

Note this setting interacts with the TextFrame.word_wrap property setting. If word wrap is
turned on, only the height of the shape will be adjusted; soft line breaks will be used to fit
the text horizontally.

### `TEXT_TO_FIT_SHAPE`

```python
TEXT_TO_FIT_SHAPE = (2, 'The font size is reduced as necessary to fit the text within the shape.')
```

The font size is reduced as necessary to fit the text within the shape.

## `MSO_TEXT_UNDERLINE_TYPE`

Bases: `BaseXmlEnum`

Indicates the type of underline for text. Used with
`.Font.underline` to specify the style of text underlining.

Alias: ``MSO_UNDERLINE``

Example::

    from pptx.enum.text import MSO_UNDERLINE

    run.font.underline = MSO_UNDERLINE.DOUBLE_LINE

MS API Name: `MsoTextUnderlineType`

http://msdn.microsoft.com/en-us/library/aa432699.aspx

### `DASH_HEAVY_LINE`

```python
DASH_HEAVY_LINE = (8, 'dashHeavy', 'Specifies a dash underline.')
```

Specifies a dash underline.

### `DASH_LINE`

```python
DASH_LINE = (7, 'dash', 'Specifies a dash line underline.')
```

Specifies a dash line underline.

### `DASH_LONG_HEAVY_LINE`

```python
DASH_LONG_HEAVY_LINE = (10, 'dashLongHeavy', 'Specifies a long heavy line underline.')
```

Specifies a long heavy line underline.

### `DASH_LONG_LINE`

```python
DASH_LONG_LINE = (9, 'dashLong', 'Specifies a dashed long line underline.')
```

Specifies a dashed long line underline.

### `DOTTED_HEAVY_LINE`

```python
DOTTED_HEAVY_LINE = (6, 'dottedHeavy', 'Specifies a dotted heavy line underline.')
```

Specifies a dotted heavy line underline.

### `DOTTED_LINE`

```python
DOTTED_LINE = (5, 'dotted', 'Specifies a dotted line underline.')
```

Specifies a dotted line underline.

### `DOT_DASH_HEAVY_LINE`

```python
DOT_DASH_HEAVY_LINE = (12, 'dotDashHeavy', 'Specifies a dot dash heavy line underline.')
```

Specifies a dot dash heavy line underline.

### `DOT_DASH_LINE`

```python
DOT_DASH_LINE = (11, 'dotDash', 'Specifies a dot dash line underline.')
```

Specifies a dot dash line underline.

### `DOT_DOT_DASH_HEAVY_LINE`

```python
DOT_DOT_DASH_HEAVY_LINE = (14, 'dotDotDashHeavy', 'Specifies a dot dot dash heavy line underline.')
```

Specifies a dot dot dash heavy line underline.

### `DOT_DOT_DASH_LINE`

```python
DOT_DOT_DASH_LINE = (13, 'dotDotDash', 'Specifies a dot dot dash line underline.')
```

Specifies a dot dot dash line underline.

### `DOUBLE_LINE`

```python
DOUBLE_LINE = (3, 'dbl', 'Specifies a double line underline.')
```

Specifies a double line underline.

### `HEAVY_LINE`

```python
HEAVY_LINE = (4, 'heavy', 'Specifies a heavy line underline.')
```

Specifies a heavy line underline.

### `MIXED`

```python
MIXED = (-2, '', 'Specifies a mix of underline types (read-only).')
```

Specifies a mix of underline types (read-only).

### `NONE`

```python
NONE = (0, 'none', 'Specifies no underline.')
```

Specifies no underline.

### `SINGLE_LINE`

```python
SINGLE_LINE = (2, 'sng', 'Specifies a single line underline.')
```

Specifies a single line underline.

### `WAVY_DOUBLE_LINE`

```python
WAVY_DOUBLE_LINE = (17, 'wavyDbl', 'Specifies a wavy double line underline.')
```

Specifies a wavy double line underline.

### `WAVY_HEAVY_LINE`

```python
WAVY_HEAVY_LINE = (16, 'wavyHeavy', 'Specifies a wavy heavy line underline.')
```

Specifies a wavy heavy line underline.

### `WAVY_LINE`

```python
WAVY_LINE = (15, 'wavy', 'Specifies a wavy line underline.')
```

Specifies a wavy line underline.

### `WORDS`

```python
WORDS = (1, 'words', 'Specifies underlining words.')
```

Specifies underlining words.

## `MSO_UNDERLINE`

```python
MSO_UNDERLINE = MSO_TEXT_UNDERLINE_TYPE
```

## `MSO_VERTICAL_ANCHOR`

Bases: `BaseXmlEnum`

Specifies the vertical alignment of text in a text frame.

Used with the `.vertical_anchor` property of the `TextFrame` object. Note that the
`vertical_anchor` property can also have the value None, indicating there is no directly
specified vertical anchor setting and its effective value is inherited from its placeholder if
it has one or from the theme. `None` may also be assigned to remove an explicitly specified
vertical anchor setting.

MS API Name: `MsoVerticalAnchor`

http://msdn.microsoft.com/en-us/library/office/ff865255.aspx

### `BOTTOM`

```python
BOTTOM = (4, 'b', 'Aligns text to bottom of text frame')
```

Aligns text to bottom of text frame

### `MIDDLE`

```python
MIDDLE = (3, 'ctr', 'Centers text vertically')
```

Centers text vertically

### `MIXED`

```python
MIXED = (-2, '', 'Return value only; indicates a combination of the other states.')
```

Return value only; indicates a combination of the other states.

### `TOP`

```python
TOP = (1, 't', 'Aligns text to top of text frame')
```

Aligns text to top of text frame

## `PP_ALIGN`

```python
PP_ALIGN = PP_PARAGRAPH_ALIGNMENT
```

## `PP_BULLET_TYPE`

Bases: `BaseEnum`

Kind of bullet explicitly set on a paragraph's own properties (paper-pptx addition).

Reported by `paragraph.bullet.type`. Reflects local `a:pPr` state only: a paragraph whose
bullet rendering is inherited from the placeholder/list-style chain reports `None`, not a
member of this enumeration.

Example::

    from pptx.enum.text import PP_BULLET_TYPE

    paragraph.bullet.set_character()
    assert paragraph.bullet.type == PP_BULLET_TYPE.CHARACTER

### `CHARACTER`

```python
CHARACTER = (1, "Character bullet (`a:buChar`), e.g. the classic '•'.")
```

Character bullet (`a:buChar`), e.g. the classic '•'.

### `NONE`

```python
NONE = (0, "Explicit 'no bullet' (`a:buNone`), overriding any inherited bullet.")
```

Explicit 'no bullet' (`a:buNone`), overriding any inherited bullet.

### `NUMBERED`

```python
NUMBERED = (2, 'Automatic numbering (`a:buAutoNum`).')
```

Automatic numbering (`a:buAutoNum`).

### `PICTURE`

```python
PICTURE = (3, 'Picture bullet (`a:buBlip`). Recognized on read; not writable in v0.')
```

Picture bullet (`a:buBlip`). Recognized on read; not writable in v0.

## `PP_PARAGRAPH_ALIGNMENT`

Bases: `BaseXmlEnum`

Specifies the horizontal alignment for one or more paragraphs.

Alias: `PP_ALIGN`

Example::

    from pptx.enum.text import PP_ALIGN

    shape.paragraphs[0].alignment = PP_ALIGN.CENTER

MS API Name: `PpParagraphAlignment`

http://msdn.microsoft.com/en-us/library/office/ff745375(v=office.15).aspx

### `CENTER`

```python
CENTER = (2, 'ctr', 'Center align')
```

Center align

### `DISTRIBUTE`

```python
DISTRIBUTE = (5, 'dist', 'Evenly distributes e.g. Japanese characters from left to right within a line')
```

Evenly distributes e.g. Japanese characters from left to right within a line

### `JUSTIFY`

```python
JUSTIFY = (4, 'just', 'Justified, i.e. each line both begins and ends at the margin.\n\nSpacing between words is adjusted such that the line exactly fills the width of the paragraph.')
```

Justified, i.e. each line both begins and ends at the margin.

Spacing between words is adjusted such that the line exactly fills the width of the paragraph.

### `JUSTIFY_LOW`

```python
JUSTIFY_LOW = (7, 'justLow', 'Justify using a small amount of space between words.')
```

Justify using a small amount of space between words.

### `LEFT`

```python
LEFT = (1, 'l', 'Left aligned')
```

Left aligned

### `MIXED`

```python
MIXED = (-2, '', 'Multiple alignments are present in a set of paragraphs (read-only).')
```

Multiple alignments are present in a set of paragraphs (read-only).

### `RIGHT`

```python
RIGHT = (3, 'r', 'Right aligned')
```

Right aligned

### `THAI_DISTRIBUTE`

```python
THAI_DISTRIBUTE = (6, 'thaiDist', 'Thai distributed')
```

Thai distributed
