<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.text.font`

Font-related proxy objects.

## `Font`

```python
Font(r: CT_R, parent: Any | None = None)
```

Bases: `ElementProxy`

Proxy object for parent of a `<w:rPr>` element and providing access to
character properties such as font name, font size, bold, and subscript.

### `all_caps`

```python
all_caps: bool | None
```

Read/write.

Causes text in this font to appear in capital letters.

### `bold`

```python
bold: bool | None
```

Read/write.

Causes text in this font to appear in bold.

### `color`

```python
color
```

A `ColorFormat` object providing a way to get and set the text color for this
font.

### `complex_script`

```python
complex_script: bool | None
```

Read/write tri-state value.

When `True`, causes the characters in the run to be treated as complex script
regardless of their Unicode values.

### `cs_bold`

```python
cs_bold: bool | None
```

Read/write tri-state value.

When `True`, causes the complex script characters in the run to be displayed in
bold typeface.

### `cs_italic`

```python
cs_italic: bool | None
```

Read/write tri-state value.

When `True`, causes the complex script characters in the run to be displayed in
italic typeface.

### `double_strike`

```python
double_strike: bool | None
```

Read/write tri-state value.

When `True`, causes the text in the run to appear with double strikethrough.

### `emboss`

```python
emboss: bool | None
```

Read/write tri-state value.

When `True`, causes the text in the run to appear as if raised off the page in
relief.

### `hidden`

```python
hidden: bool | None
```

Read/write tri-state value.

When `True`, causes the text in the run to be hidden from display, unless
applications settings force hidden text to be shown.

### `highlight_color`

```python
highlight_color: WD_COLOR_INDEX | None
```

Color of highlighing applied or `None` if not highlighted.

### `imprint`

```python
imprint: bool | None
```

Read/write tri-state value.

When `True`, causes the text in the run to appear as if pressed into the page.

### `italic`

```python
italic: bool | None
```

Read/write tri-state value.

When `True`, causes the text of the run to appear in italics. `None` indicates
the effective value is inherited from the style hierarchy.

### `math`

```python
math: bool | None
```

Read/write tri-state value.

When `True`, specifies this run contains WML that should be handled as though it
was Office Open XML Math.

### `name`

```python
name: str | None
```

The typeface name for this `Font`.

Causes the text it controls to appear in the named font, if a matching font is
found. `None` indicates the typeface is inherited from the style hierarchy.

### `no_proof`

```python
no_proof: bool | None
```

Read/write tri-state value.

When `True`, specifies that the contents of this run should not report any
errors when the document is scanned for spelling and grammar.

### `outline`

```python
outline: bool | None
```

Read/write tri-state value.

When `True` causes the characters in the run to appear as if they have an
outline, by drawing a one pixel wide border around the inside and outside
borders of each character glyph.

### `rtl`

```python
rtl: bool | None
```

Read/write tri-state value.

When `True` causes the text in the run to have right-to-left characteristics.

### `shadow`

```python
shadow: bool | None
```

Read/write tri-state value.

When `True` causes the text in the run to appear as if each character has a
shadow.

### `size`

```python
size: Length | None
```

Font height in English Metric Units (EMU).

`None` indicates the font size should be inherited from the style hierarchy.
`Length` is a subclass of `int` having properties for convenient conversion into
points or other length units. The `docx.shared.Pt` class allows
convenient specification of point values::

    >>> font.size = Pt(24)
    >>> font.size
    304800
    >>> font.size.pt
    24.0

### `small_caps`

```python
small_caps: bool | None
```

Read/write tri-state value.

When `True` causes the lowercase characters in the run to appear as capital
letters two points smaller than the font size specified for the run.

### `snap_to_grid`

```python
snap_to_grid: bool | None
```

Read/write tri-state value.

When `True` causes the run to use the document grid characters per line settings
defined in the docGrid element when laying out the characters in this run.

### `spec_vanish`

```python
spec_vanish: bool | None
```

Read/write tri-state value.

When `True`, specifies that the given run shall always behave as if it is
hidden, even when hidden text is being displayed in the current document. The
property has a very narrow, specialized use related to the table of contents.
Consult the spec (§17.3.2.36) for more details.

### `strike`

```python
strike: bool | None
```

Read/write tri-state value.

When `True` causes the text in the run to appear with a single horizontal line
through the center of the line.

### `subscript`

```python
subscript: bool | None
```

Boolean indicating whether the characters in this `Font` appear as subscript.

`None` indicates the subscript/subscript value is inherited from the style
hierarchy.

### `superscript`

```python
superscript: bool | None
```

Boolean indicating whether the characters in this `Font` appear as
superscript.

`None` indicates the subscript/superscript value is inherited from the style
hierarchy.

### `underline`

```python
underline: bool | WD_UNDERLINE | None
```

The underline style for this `Font`.

The value is one of `None`, `True`, `False`, or a member of `WdUnderline`.

`None` indicates the font inherits its underline value from the style hierarchy.
`False` indicates no underline. `True` indicates single underline. The values
from `WdUnderline` are used to specify other outline styles such as double,
wavy, and dotted.

### `web_hidden`

```python
web_hidden: bool | None
```

Read/write tri-state value.

When `True`, specifies that the contents of this run shall be hidden when the
document is displayed in web page view.
