<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.enum.text`

Enumerations related to text in WordprocessingML files.

## `WD_ALIGN_PARAGRAPH`

```python
WD_ALIGN_PARAGRAPH = WD_PARAGRAPH_ALIGNMENT
```

## `WD_BREAK`

```python
WD_BREAK = WD_BREAK_TYPE
```

## `WD_BREAK_TYPE`

Bases: `Enum`

Corresponds to WdBreakType enumeration.

http://msdn.microsoft.com/en-us/library/office/ff195905.aspx.

### `COLUMN`

```python
COLUMN = 8
```

### `LINE`

```python
LINE = 6
```

### `LINE_CLEAR_ALL`

```python
LINE_CLEAR_ALL = 11
```

### `LINE_CLEAR_LEFT`

```python
LINE_CLEAR_LEFT = 9
```

### `LINE_CLEAR_RIGHT`

```python
LINE_CLEAR_RIGHT = 10
```

### `PAGE`

```python
PAGE = 7
```

### `SECTION_CONTINUOUS`

```python
SECTION_CONTINUOUS = 3
```

### `SECTION_EVEN_PAGE`

```python
SECTION_EVEN_PAGE = 4
```

### `SECTION_NEXT_PAGE`

```python
SECTION_NEXT_PAGE = 2
```

### `SECTION_ODD_PAGE`

```python
SECTION_ODD_PAGE = 5
```

### `TEXT_WRAPPING`

```python
TEXT_WRAPPING = 11
```

## `WD_COLOR`

```python
WD_COLOR = WD_COLOR_INDEX
```

## `WD_COLOR_INDEX`

Bases: `BaseXmlEnum`

Specifies a standard preset color to apply.

Used for font highlighting and perhaps other applications.

* MS API name: `WdColorIndex`
* URL: https://msdn.microsoft.com/EN-US/library/office/ff195343.aspx

### `AUTO`

```python
AUTO = (0, 'default', 'Automatic color. Default; usually black.')
```

Automatic color. Default; usually black.

### `BLACK`

```python
BLACK = (1, 'black', 'Black color.')
```

Black color.

### `BLUE`

```python
BLUE = (2, 'blue', 'Blue color')
```

Blue color

### `BRIGHT_GREEN`

```python
BRIGHT_GREEN = (4, 'green', 'Bright green color.')
```

Bright green color.

### `DARK_BLUE`

```python
DARK_BLUE = (9, 'darkBlue', 'Dark blue color.')
```

Dark blue color.

### `DARK_RED`

```python
DARK_RED = (13, 'darkRed', 'Dark red color.')
```

Dark red color.

### `DARK_YELLOW`

```python
DARK_YELLOW = (14, 'darkYellow', 'Dark yellow color.')
```

Dark yellow color.

### `GRAY_25`

```python
GRAY_25 = (16, 'lightGray', '25% shade of gray color.')
```

25% shade of gray color.

### `GRAY_50`

```python
GRAY_50 = (15, 'darkGray', '50% shade of gray color.')
```

50% shade of gray color.

### `GREEN`

```python
GREEN = (11, 'darkGreen', 'Green color.')
```

Green color.

### `INHERITED`

```python
INHERITED = (-1, None, 'Color is inherited from the style hierarchy.')
```

Color is inherited from the style hierarchy.

### `PINK`

```python
PINK = (5, 'magenta', 'Pink color.')
```

Pink color.

### `RED`

```python
RED = (6, 'red', 'Red color.')
```

Red color.

### `TEAL`

```python
TEAL = (10, 'darkCyan', 'Teal color.')
```

Teal color.

### `TURQUOISE`

```python
TURQUOISE = (3, 'cyan', 'Turquoise color.')
```

Turquoise color.

### `VIOLET`

```python
VIOLET = (12, 'darkMagenta', 'Violet color.')
```

Violet color.

### `WHITE`

```python
WHITE = (8, 'white', 'White color.')
```

White color.

### `YELLOW`

```python
YELLOW = (7, 'yellow', 'Yellow color.')
```

Yellow color.

## `WD_LINE_SPACING`

Bases: `BaseXmlEnum`

Specifies a line spacing format to be applied to a paragraph.

Example::

    from docx.enum.text import WD_LINE_SPACING

    paragraph = document.add_paragraph()
    paragraph.line_spacing_rule = WD_LINE_SPACING.EXACTLY


MS API name: `WdLineSpacing`

URL: http://msdn.microsoft.com/en-us/library/office/ff844910.aspx

### `AT_LEAST`

```python
AT_LEAST = (3, 'atLeast', 'Minimum line spacing is specified amount. Amount is specified separately.')
```

Minimum line spacing is specified amount. Amount is specified separately.

### `DOUBLE`

```python
DOUBLE = (2, 'UNMAPPED', 'Double spaced.')
```

Double spaced.

### `EXACTLY`

```python
EXACTLY = (4, 'exact', 'Line spacing is exactly specified amount. Amount is specified separately.')
```

Line spacing is exactly specified amount. Amount is specified separately.

### `MULTIPLE`

```python
MULTIPLE = (5, 'auto', 'Line spacing is specified as multiple of line heights. Changing font size will change line spacing proportionately.')
```

Line spacing is specified as multiple of line heights. Changing font size will
change the line spacing proportionately.

### `ONE_POINT_FIVE`

```python
ONE_POINT_FIVE = (1, 'UNMAPPED', 'Space-and-a-half line spacing.')
```

Space-and-a-half line spacing.

### `SINGLE`

```python
SINGLE = (0, 'UNMAPPED', 'Single spaced (default).')
```

Single spaced (default).

## `WD_PARAGRAPH_ALIGNMENT`

Bases: `BaseXmlEnum`

Alias: **WD_ALIGN_PARAGRAPH**

Specifies paragraph justification type.

Example::

    from docx.enum.text import WD_ALIGN_PARAGRAPH

    paragraph = document.add_paragraph()
    paragraph.alignment = WD_ALIGN_PARAGRAPH.CENTER

### `CENTER`

```python
CENTER = (1, 'center', 'Center-aligned.')
```

Center-aligned.

### `DISTRIBUTE`

```python
DISTRIBUTE = (4, 'distribute', 'Paragraph characters are distributed to fill entire width of paragraph.')
```

Paragraph characters are distributed to fill entire width of paragraph.

### `JUSTIFY`

```python
JUSTIFY = (3, 'both', 'Fully justified.')
```

Fully justified.

### `JUSTIFY_HI`

```python
JUSTIFY_HI = (7, 'highKashida', 'Justified with a high character compression ratio.')
```

Justified with a high character compression ratio.

### `JUSTIFY_LOW`

```python
JUSTIFY_LOW = (8, 'lowKashida', 'Justified with a low character compression ratio.')
```

Justified with a low character compression ratio.

### `JUSTIFY_MED`

```python
JUSTIFY_MED = (5, 'mediumKashida', 'Justified with a medium character compression ratio.')
```

Justified with a medium character compression ratio.

### `LEFT`

```python
LEFT = (0, 'left', 'Left-aligned')
```

Left-aligned

### `RIGHT`

```python
RIGHT = (2, 'right', 'Right-aligned.')
```

Right-aligned.

### `THAI_JUSTIFY`

```python
THAI_JUSTIFY = (9, 'thaiDistribute', 'Justified according to Thai formatting layout.')
```

Justified according to Thai formatting layout.

## `WD_TAB_ALIGNMENT`

Bases: `BaseXmlEnum`

Specifies the tab stop alignment to apply.

MS API name: `WdTabAlignment`

URL: https://msdn.microsoft.com/EN-US/library/office/ff195609.aspx

### `BAR`

```python
BAR = (4, 'bar', 'Bar-aligned.')
```

Bar-aligned.

### `CENTER`

```python
CENTER = (1, 'center', 'Center-aligned.')
```

Center-aligned.

### `CLEAR`

```python
CLEAR = (101, 'clear', 'Clear an inherited tab stop.')
```

Clear an inherited tab stop.

### `DECIMAL`

```python
DECIMAL = (3, 'decimal', 'Decimal-aligned.')
```

Decimal-aligned.

### `END`

```python
END = (102, 'end', 'Right-aligned.  (deprecated)')
```

Right-aligned.  (deprecated)

### `LEFT`

```python
LEFT = (0, 'left', 'Left-aligned.')
```

Left-aligned.

### `LIST`

```python
LIST = (6, 'list', 'List-aligned. (deprecated)')
```

List-aligned. (deprecated)

### `NUM`

```python
NUM = (103, 'num', 'Left-aligned.  (deprecated)')
```

Left-aligned.  (deprecated)

### `RIGHT`

```python
RIGHT = (2, 'right', 'Right-aligned.')
```

Right-aligned.

### `START`

```python
START = (104, 'start', 'Left-aligned.  (deprecated)')
```

Left-aligned.  (deprecated)

## `WD_TAB_LEADER`

Bases: `BaseXmlEnum`

Specifies the character to use as the leader with formatted tabs.

MS API name: `WdTabLeader`

URL: https://msdn.microsoft.com/en-us/library/office/ff845050.aspx

### `DASHES`

```python
DASHES = (2, 'hyphen', 'Dashes.')
```

Dashes.

### `DOTS`

```python
DOTS = (1, 'dot', 'Dots.')
```

Dots.

### `HEAVY`

```python
HEAVY = (4, 'heavy', 'A heavy line.')
```

A heavy line.

### `LINES`

```python
LINES = (3, 'underscore', 'Double lines.')
```

Double lines.

### `MIDDLE_DOT`

```python
MIDDLE_DOT = (5, 'middleDot', 'A vertically-centered dot.')
```

A vertically-centered dot.

### `SPACES`

```python
SPACES = (0, 'none', 'Spaces. Default.')
```

Spaces. Default.

## `WD_UNDERLINE`

Bases: `BaseXmlEnum`

Specifies the style of underline applied to a run of characters.

MS API name: `WdUnderline`

URL: http://msdn.microsoft.com/en-us/library/office/ff822388.aspx

### `DASH`

```python
DASH = (7, 'dash', 'Dashes.')
```

Dashes.

### `DASH_HEAVY`

```python
DASH_HEAVY = (23, 'dashedHeavy', 'Heavy dashes.')
```

Heavy dashes.

### `DASH_LONG`

```python
DASH_LONG = (39, 'dashLong', 'Long dashes.')
```

Long dashes.

### `DASH_LONG_HEAVY`

```python
DASH_LONG_HEAVY = (55, 'dashLongHeavy', 'Long heavy dashes.')
```

Long heavy dashes.

### `DOTTED`

```python
DOTTED = (4, 'dotted', 'Dots.')
```

Dots.

### `DOTTED_HEAVY`

```python
DOTTED_HEAVY = (20, 'dottedHeavy', 'Heavy dots.')
```

Heavy dots.

### `DOT_DASH`

```python
DOT_DASH = (9, 'dotDash', 'Alternating dots and dashes.')
```

Alternating dots and dashes.

### `DOT_DASH_HEAVY`

```python
DOT_DASH_HEAVY = (25, 'dashDotHeavy', 'Alternating heavy dots and heavy dashes.')
```

Alternating heavy dots and heavy dashes.

### `DOT_DOT_DASH`

```python
DOT_DOT_DASH = (10, 'dotDotDash', 'An alternating dot-dot-dash pattern.')
```

An alternating dot-dot-dash pattern.

### `DOT_DOT_DASH_HEAVY`

```python
DOT_DOT_DASH_HEAVY = (26, 'dashDotDotHeavy', 'An alternating heavy dot-dot-dash pattern.')
```

An alternating heavy dot-dot-dash pattern.

### `DOUBLE`

```python
DOUBLE = (3, 'double', 'A double line.')
```

A double line.

### `INHERITED`

```python
INHERITED = (-1, None, 'Inherit underline setting from containing paragraph.')
```

Inherit underline setting from containing paragraph.

### `NONE`

```python
NONE = (0, 'none', 'No underline.\n\nThis setting overrides any inherited underline value, so can be used to remove underline from a run that inherits underlining from its containing paragraph. Note this is not the same as assigning `None` to Run.underline. `None` is a valid assignment value, but causes the run to inherit its underline value. Assigning `WD_UNDERLINE.NONE` causes underlining to be unconditionally turned off.')
```

No underline.

This setting overrides any inherited underline value, so can be used to remove
underline from a run that inherits underlining from its containing paragraph. Note
this is not the same as assigning `None` to Run.underline. `None` is a valid
assignment value, but causes the run to inherit its underline value. Assigning
``WD_UNDERLINE.NONE`` causes underlining to be unconditionally turned off.

### `SINGLE`

```python
SINGLE = (1, 'single', 'A single line.\n\nNote that this setting is write-only in the sense that `True` (rather than `WD_UNDERLINE.SINGLE`) is returned for a run having this setting.')
```

A single line.

Note that this setting is write-only in the sense that `True`
(rather than ``WD_UNDERLINE.SINGLE``) is returned for a run having this setting.

### `THICK`

```python
THICK = (6, 'thick', 'A single thick line.')
```

A single thick line.

### `WAVY`

```python
WAVY = (11, 'wave', 'A single wavy line.')
```

A single wavy line.

### `WAVY_DOUBLE`

```python
WAVY_DOUBLE = (43, 'wavyDouble', 'A double wavy line.')
```

A double wavy line.

### `WAVY_HEAVY`

```python
WAVY_HEAVY = (27, 'wavyHeavy', 'A heavy wavy line.')
```

A heavy wavy line.

### `WORDS`

```python
WORDS = (2, 'words', 'Underline individual words only.')
```

Underline individual words only.
