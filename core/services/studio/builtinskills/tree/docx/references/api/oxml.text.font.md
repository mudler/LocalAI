<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.text.font`

Custom element classes related to run properties (font).

## `CT_Color`

Bases: `BaseOxmlElement`

`w:color` element, specifying the color of a font and perhaps other objects.

### `themeColor`

```python
themeColor: MSO_THEME_COLOR | None = OptionalAttribute('w:themeColor', MSO_THEME_COLOR)
```

### `val`

```python
val: RGBColor | str = RequiredAttribute('w:val', ST_HexColor)
```

## `CT_Fonts`

Bases: `BaseOxmlElement`

`<w:rFonts>` element.

Specifies typeface name for the various language types.

### `ascii`

```python
ascii: str | None = OptionalAttribute('w:ascii', ST_String)
```

### `hAnsi`

```python
hAnsi: str | None = OptionalAttribute('w:hAnsi', ST_String)
```

## `CT_Highlight`

Bases: `BaseOxmlElement`

`w:highlight` element, specifying font highlighting/background color.

### `val`

```python
val: WD_COLOR_INDEX = RequiredAttribute('w:val', WD_COLOR_INDEX)
```

## `CT_HpsMeasure`

Bases: `BaseOxmlElement`

Used for `<w:sz>` element and others, specifying font size in half-points.

### `val`

```python
val: Length = RequiredAttribute('w:val', ST_HpsMeasure)
```

## `CT_RPr`

Bases: `BaseOxmlElement`

`<w:rPr>` element, containing the properties for a run.

### `b`

```python
b: CT_OnOff | None = ZeroOrOne('w:b', successors=_tag_seq[3:])
```

### `bCs`

```python
bCs = ZeroOrOne('w:bCs', successors=_tag_seq[4:])
```

### `caps`

```python
caps = ZeroOrOne('w:caps', successors=_tag_seq[7:])
```

### `color`

```python
color: CT_Color | None = ZeroOrOne('w:color', successors=_tag_seq[19:])
```

### `cs`

```python
cs = ZeroOrOne('w:cs', successors=_tag_seq[34:])
```

### `dstrike`

```python
dstrike = ZeroOrOne('w:dstrike', successors=_tag_seq[10:])
```

### `emboss`

```python
emboss = ZeroOrOne('w:emboss', successors=_tag_seq[13:])
```

### `get_or_add_color`

```python
get_or_add_color: Callable[[], CT_Color]
```

### `get_or_add_highlight`

```python
get_or_add_highlight: Callable[[], CT_Highlight]
```

### `get_or_add_rFonts`

```python
get_or_add_rFonts: Callable[[], CT_Fonts]
```

### `get_or_add_sz`

```python
get_or_add_sz: Callable[[], CT_HpsMeasure]
```

### `get_or_add_vertAlign`

```python
get_or_add_vertAlign: Callable[[], CT_VerticalAlignRun]
```

### `highlight`

```python
highlight: CT_Highlight | None = ZeroOrOne('w:highlight', successors=_tag_seq[26:])
```

### `highlight_val`

```python
highlight_val: WD_COLOR_INDEX | None
```

Value of `./w:highlight/@val`.

Specifies font's highlight color, or `None` if the text is not highlighted.

### `i`

```python
i = ZeroOrOne('w:i', successors=_tag_seq[5:])
```

### `iCs`

```python
iCs = ZeroOrOne('w:iCs', successors=_tag_seq[6:])
```

### `imprint`

```python
imprint = ZeroOrOne('w:imprint', successors=_tag_seq[14:])
```

### `noProof`

```python
noProof = ZeroOrOne('w:noProof', successors=_tag_seq[15:])
```

### `oMath`

```python
oMath = ZeroOrOne('w:oMath', successors=_tag_seq[39:])
```

### `outline`

```python
outline = ZeroOrOne('w:outline', successors=_tag_seq[11:])
```

### `rFonts`

```python
rFonts: CT_Fonts | None = ZeroOrOne('w:rFonts', successors=_tag_seq[2:])
```

### `rFonts_ascii`

```python
rFonts_ascii: str | None
```

The value of `w:rFonts/@w:ascii` or `None` if not present.

Represents the assigned typeface name. The rFonts element also specifies other
special-case typeface names; this method handles the case where just the common
name is required.

### `rFonts_hAnsi`

```python
rFonts_hAnsi: str | None
```

The value of `w:rFonts/@w:hAnsi` or `None` if not present.

### `rStyle`

```python
rStyle: CT_String | None = ZeroOrOne('w:rStyle', successors=_tag_seq[1:])
```

### `rtl`

```python
rtl = ZeroOrOne('w:rtl', successors=_tag_seq[33:])
```

### `shadow`

```python
shadow = ZeroOrOne('w:shadow', successors=_tag_seq[12:])
```

### `smallCaps`

```python
smallCaps = ZeroOrOne('w:smallCaps', successors=_tag_seq[8:])
```

### `snapToGrid`

```python
snapToGrid = ZeroOrOne('w:snapToGrid', successors=_tag_seq[16:])
```

### `specVanish`

```python
specVanish = ZeroOrOne('w:specVanish', successors=_tag_seq[38:])
```

### `strike`

```python
strike = ZeroOrOne('w:strike', successors=_tag_seq[9:])
```

### `style`

```python
style: str | None
```

String in `./w:rStyle/@val`, or None if `w:rStyle` is not present.

### `subscript`

```python
subscript: bool | None
```

`True` if `./w:vertAlign/@w:val` is "subscript".

`False` if `w:vertAlign/@w:val` contains any other value. `None` if
`w:vertAlign` is not present.

### `superscript`

```python
superscript: bool | None
```

`True` if `w:vertAlign/@w:val` is 'superscript'.

`False` if `w:vertAlign/@w:val` contains any other value. `None` if
`w:vertAlign` is not present.

### `sz`

```python
sz: CT_HpsMeasure | None = ZeroOrOne('w:sz', successors=_tag_seq[24:])
```

### `sz_val`

```python
sz_val: Length | None
```

The value of `w:sz/@w:val` or `None` if not present.

### `u`

```python
u: CT_Underline | None = ZeroOrOne('w:u', successors=_tag_seq[27:])
```

### `u_val`

```python
u_val: WD_UNDERLINE | None
```

Value of `w:u/@val`, or None if not present.

Values `WD_UNDERLINE.SINGLE` and `WD_UNDERLINE.NONE` are mapped to `True` and
`False` respectively.

### `vanish`

```python
vanish = ZeroOrOne('w:vanish', successors=_tag_seq[17:])
```

### `vertAlign`

```python
vertAlign: CT_VerticalAlignRun | None = ZeroOrOne('w:vertAlign', successors=_tag_seq[32:])
```

### `webHidden`

```python
webHidden = ZeroOrOne('w:webHidden', successors=_tag_seq[18:])
```

## `CT_Underline`

Bases: `BaseOxmlElement`

`<w:u>` element, specifying the underlining style for a run.

### `val`

```python
val: WD_UNDERLINE | None = OptionalAttribute('w:val', WD_UNDERLINE)
```

## `CT_VerticalAlignRun`

Bases: `BaseOxmlElement`

`<w:vertAlign>` element, specifying subscript or superscript.

### `val`

```python
val: str = RequiredAttribute('w:val', ST_VerticalAlignRun)
```
