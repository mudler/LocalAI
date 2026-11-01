<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.text.text`

Text-related objects such as TextFrame and Paragraph.

## `Font`

```python
Font(rPr: CT_TextCharacterProperties)
```

Bases: `object`

Character properties object, providing font size, font name, bold, italic, etc.

Corresponds to `a:rPr` child element of a run. Also appears as `a:defRPr` and
`a:endParaRPr` in paragraph and `a:defRPr` in list style elements.

### `bold`

```python
bold: bool | None
```

Get or set boolean bold value of `Font`, e.g. `paragraph.font.bold = True`.

If set to `None`, the bold setting is cleared and is inherited from an enclosing shape's
setting, or a setting in a style or master. Returns None if no bold attribute is present,
meaning the effective bold value is inherited from a master or the theme.

### `color`

```python
color() -> ColorFormat
```

The `ColorFormat` instance that provides access to the color settings for this font.

### `fill`

```python
fill() -> FillFormat
```

`FillFormat` instance for this font.

Provides access to fill properties such as fill color.

### `italic`

```python
italic: bool | None
```

Get or set boolean italic value of `Font` instance.

Has the same behaviors as bold with respect to None values.

### `language_id`

```python
language_id: MSO_LANGUAGE_ID | None
```

Get or set the language id of this `Font` instance.

The language id is a member of the `MsoLanguageId` enumeration. Assigning `None`
removes any language setting, the same behavior as assigning `MSO_LANGUAGE_ID.NONE`.

### `name`

```python
name: str | None
```

Get or set the typeface name for this `Font` instance.

Causes the text it controls to appear in the named font, if a matching font is found.
Returns `None` if the typeface is currently inherited from the theme. Setting it to `None`
removes any override of the theme typeface.

### `size`

```python
size: Length | None
```

Indicates the font height in English Metric Units (EMU).

Read/write. `None` indicates the font size should be inherited from its style hierarchy,
such as a placeholder or document defaults (usually 18pt). `Length` is a subclass of `int`
having properties for convenient conversion into points or other length units. Likewise,
the `pptx.util.Pt` class allows convenient specification of point values::

    >>> font.size = Pt(24)
    >>> font.size
    304800
    >>> font.size.pt
    24.0

### `underline`

```python
underline: bool | MSO_TEXT_UNDERLINE_TYPE | None
```

Indicaties the underline setting for this font.

Value is `True`, `False`, `None`, or a member of the `MsoTextUnderlineType`
enumeration. `None` is the default and indicates the underline setting should be inherited
from the style hierarchy, such as from a placeholder. `True` indicates single underline.
`False` indicates no underline. Other settings such as double and wavy underlining are
indicated with members of the `MsoTextUnderlineType` enumeration.

## `TextFrame`

```python
TextFrame(txBody: CT_TextBody, parent: ProvidesPart)
```

Bases: `Subshape`

The part of a shape that contains its text.

Not all shapes have a text frame. Corresponds to the `p:txBody` element that can
appear as a child element of `p:sp`. Not intended to be constructed directly.

### `add_paragraph`

```python
add_paragraph()
```

Return new `_Paragraph` instance appended to the sequence of
paragraphs contained in this text frame.

### `auto_size`

```python
auto_size: MSO_AUTO_SIZE | None
```

Resizing strategy used to fit text within this shape.

Determins the type of automatic resizing used to fit the text of this shape within its
bounding box when the text would otherwise extend beyond the shape boundaries. May be
`None`, `MSO_AUTO_SIZE.NONE`, `MSO_AUTO_SIZE.SHAPE_TO_FIT_TEXT`, or
`MSO_AUTO_SIZE.TEXT_TO_FIT_SHAPE`.

### `clear`

```python
clear()
```

Remove all paragraphs except one empty one.

### `fit_text`

```python
fit_text(font_family: str = 'Calibri', max_size: int = 18, bold: bool = False, italic: bool = False, font_file: str | None = None)
```

Fit text-frame text entirely within bounds of its shape.

Make the text in this text frame fit entirely within the bounds of its shape by setting
word wrap on and applying the "best-fit" font size to all the text it contains.

`TextFrame.auto_size` is set to `MSO_AUTO_SIZE.NONE`. The font size will not
be set larger than `max_size` points. If the path to a matching TrueType font is provided
as `font_file`, that font file will be used for the font metrics. If `font_file` is `None`,
best efforts are made to locate a font file with matchhing `font_family`, `bold`, and
`italic` installed on the current system (usually succeeds if the font is installed).

### `font_scale`

```python
font_scale: float | None
```

Font scale percent of this frame's `a:normAutofit`, e.g. 62.5 (paper-pptx addition).

100.0 when the frame has `a:normAutofit` with no explicit scale; `None` when the
frame's autofit setting is anything other than `a:normAutofit`. Read-only: PowerPoint
owns this value (it records the shrink-to-fit reduction last applied); use
`normalize_autofit` to freeze it into explicit sizes.

### `line_space_reduction`

```python
line_space_reduction: float | None
```

Line-spacing reduction percent of `a:normAutofit`, e.g. 20.0 (paper-pptx addition).

0.0 when the frame has `a:normAutofit` with no explicit reduction; `None` when the
frame's autofit setting is anything other than `a:normAutofit`. Read-only.

### `margin_bottom`

```python
margin_bottom: Length
```

`Length` value representing the inset of text from the bottom text frame border.

`pptx.util.Inches` provides a convenient way of setting the value, e.g.
`text_frame.margin_bottom = Inches(0.05)`.

### `margin_left`

```python
margin_left: Length
```

Inset of text from left text frame border as `Length` value.

### `margin_right`

```python
margin_right: Length
```

Inset of text from right text frame border as `Length` value.

### `margin_top`

```python
margin_top: Length
```

Inset of text from top text frame border as `Length` value.

### `normalize_autofit`

```python
normalize_autofit(*, min_font_size: Length | None = None, resolve: bool = False) -> None
```

Freeze this frame's rendered text metrics and set explicit no-autofit.

paper-pptx addition. What the reader currently sees is made explicit, then the frame's
autofit is set to `a:noAutofit`:

- `a:normAutofit` with a font scale: every explicit font size in the frame (run,
  paragraph-default, and end-paragraph properties) is multiplied by the scale. If any
  run's size is not locally resolvable (neither its own `sz` nor its paragraph's
  default), `UnsupportedStructureError` is raised — unless `resolve=True`, in which
  case the size is resolved through the effective-style walk (placeholder → layout →
  master → theme) and frozen from the resolved value; what the walk cannot resolve
  still refuses. This API never silently guesses.
- `a:normAutofit` with a line-spacing reduction: every paragraph's explicit line
  spacing is reduced accordingly; any paragraph without explicit line spacing raises
  `UnsupportedStructureError` (`resolve` covers font sizes only in this version).
- `a:spAutoFit`, `a:noAutofit`, or no autofit element: no text metrics change.

`min_font_size` (a `Length`, e.g. `Pt(11)`) is applied after freezing: explicit sizes
below the floor are raised to it. Validation completes fully before the first write
(a refusal leaves the frame byte-identical).

### `paragraphs`

```python
paragraphs: tuple[_Paragraph, ...]
```

Sequence of paragraphs in this text frame.

A text frame always contains at least one paragraph.

### `text`

```python
text: str
```

All text in this text-frame as a single string.

Read/write. The return value contains all text in this text-frame. A line-feed character
(`"\n"`) separates the text for each paragraph. A vertical-tab character (`"\v"`) appears
for each line break (aka. soft carriage-return) encountered.

The vertical-tab character is how PowerPoint represents a soft carriage return in clipboard
text, which is why that encoding was chosen.

Assignment replaces all text in the text frame. A new paragraph is added for each line-feed
character (`"\n"`) encountered. A line-break (soft carriage-return) is inserted for each
vertical-tab character (`"\v"`) encountered.

Any control character other than newline, tab, or vertical-tab are escaped as plain-text
like "_x001B_" (for ESC (ASCII 32) in this example).

### `vertical_anchor`

```python
vertical_anchor: MSO_VERTICAL_ANCHOR | None
```

Represents the vertical alignment of text in this text frame.

`None` indicates the effective value should be inherited from this object's style hierarchy.

### `word_wrap`

```python
word_wrap: bool | None
```

`True` when lines of text in this shape are wrapped to fit within the shape's width.

Read-write. Valid values are True, False, or None. True and False turn word wrap on and
off, respectively. Assigning None to word wrap causes any word wrap setting to be removed
from the text frame, causing it to inherit this setting from its style hierarchy.
