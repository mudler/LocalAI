<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.text`

Custom element classes for text-related XML elements

## `CT_RegularTextRun`

Bases: `BaseOxmlElement`

`a:r` custom element class

### `get_or_add_rPr`

```python
get_or_add_rPr: Callable[[], CT_TextCharacterProperties]
```

### `rPr`

```python
rPr: CT_TextCharacterProperties | None = ZeroOrOne('a:rPr', successors=('a:t',))
```

### `t`

```python
t: BaseOxmlElement = OneAndOnlyOne('a:t')
```

### `text`

```python
text: str
```

All text of (required) `a:t` child.

## `CT_TextAutonumberBullet`

Bases: `BaseOxmlElement`

`a:buAutoNum` element, specifying automatic numbering for a paragraph.

### `startAt`

```python
startAt: int = OptionalAttribute('startAt', ST_TextBulletStartAtNum, default=1)
```

### `type`

```python
type: str = RequiredAttribute('type', ST_TextAutonumberScheme)
```

## `CT_TextBody`

Bases: `BaseOxmlElement`

`p:txBody` custom element class.

Also used for `c:txPr` in charts and perhaps other elements.

### `add_p`

```python
add_p: Callable[[], CT_TextParagraph]
```

### `bodyPr`

```python
bodyPr: CT_TextBodyProperties = OneAndOnlyOne('a:bodyPr')
```

### `clear_content`

```python
clear_content()
```

Remove all `a:p` children, but leave any others.

cf. lxml `_Element.clear()` method which removes all children.

### `defRPr`

```python
defRPr: CT_TextCharacterProperties
```

`a:defRPr` element of required first `p` child, added with its ancestors if not present.

Used when element is a ``c:txPr`` in a chart and the `p` element is used only to specify
formatting, not content.

### `is_empty`

```python
is_empty: bool
```

True if only a single empty `a:p` element is present.

### `lstStyle`

```python
lstStyle: CT_TextListStyle | None = ZeroOrOne('a:lstStyle', successors=('a:p',))
```

### `new`

```python
new()
```

Return a new `p:txBody` element tree.

### `new_a_txBody`

```python
new_a_txBody() -> CT_TextBody
```

Return a new `a:txBody` element tree.

Suitable for use in a table cell and possibly other situations.

### `new_p_txBody`

```python
new_p_txBody()
```

Return a new `p:txBody` element tree, suitable for use in an `p:sp` element.

### `new_txPr`

```python
new_txPr()
```

Return a `c:txPr` element tree.

Suitable for use in a chart object like data labels or tick labels.

### `p`

```python
p: CT_TextParagraph = OneOrMore('a:p')
```

### `p_lst`

```python
p_lst: list[CT_TextParagraph]
```

### `unclear_content`

```python
unclear_content()
```

Ensure p:txBody has at least one a:p child.

Intuitively, reverse a ".clear_content()" operation to minimum conformance with spec
(single empty paragraph).

## `CT_TextBodyProperties`

Bases: `BaseOxmlElement`

`a:bodyPr` custom element class.

### `anchor`

```python
anchor: MSO_VERTICAL_ANCHOR | None = OptionalAttribute('anchor', MSO_VERTICAL_ANCHOR)
```

### `autofit`

```python
autofit
```

The autofit setting for the text frame, a member of the `MSO_AUTO_SIZE` enumeration.

### `bIns`

```python
bIns: Length = OptionalAttribute('bIns', ST_Coordinate32, default=Emu(45720))
```

### `eg_textAutoFit`

```python
eg_textAutoFit = ZeroOrOneChoice((Choice('a:noAutofit'), Choice('a:normAutofit'), Choice('a:spAutoFit')), successors=('a:scene3d', 'a:sp3d', 'a:flatTx', 'a:extLst'))
```

### `lIns`

```python
lIns: Length = OptionalAttribute('lIns', ST_Coordinate32, default=Emu(91440))
```

### `noAutofit`

```python
noAutofit: BaseOxmlElement | None
```

### `normAutofit`

```python
normAutofit: CT_TextNormalAutofit | None
```

### `rIns`

```python
rIns: Length = OptionalAttribute('rIns', ST_Coordinate32, default=Emu(91440))
```

### `spAutoFit`

```python
spAutoFit: BaseOxmlElement | None
```

### `tIns`

```python
tIns: Length = OptionalAttribute('tIns', ST_Coordinate32, default=Emu(45720))
```

### `wrap`

```python
wrap: str | None = OptionalAttribute('wrap', ST_TextWrappingType)
```

## `CT_TextBulletSizePercent`

Bases: `BaseOxmlElement`

`a:buSzPct` element, bullet size as a fraction of the paragraph's text size.

### `val`

```python
val: float = RequiredAttribute('val', ST_TextBulletSizePercent)
```

## `CT_TextCharBullet`

Bases: `BaseOxmlElement`

`a:buChar` element, specifying the character to use as a paragraph's bullet.

### `char`

```python
char: str = RequiredAttribute('char', XsdString)
```

## `CT_TextCharacterProperties`

Bases: `BaseOxmlElement`

Custom element class for `a:rPr`, `a:defRPr`, and `a:endParaRPr`.

'rPr' is short for 'run properties', and it corresponds to the `Font` proxy class.

### `add_hlinkClick`

```python
add_hlinkClick(rId: str) -> CT_Hyperlink
```

Add an `a:hlinkClick` child element with r:id attribute set to `rId`.

### `b`

```python
b: bool | None = OptionalAttribute('b', XsdBoolean)
```

### `eg_fillProperties`

```python
eg_fillProperties = ZeroOrOneChoice((Choice('a:noFill'), Choice('a:solidFill'), Choice('a:gradFill'), Choice('a:blipFill'), Choice('a:pattFill'), Choice('a:grpFill')), successors=('a:effectLst', 'a:effectDag', 'a:highlight', 'a:uLnTx', 'a:uLn', 'a:uFillTx', 'a:uFill', 'a:latin', 'a:ea', 'a:cs', 'a:sym', 'a:hlinkClick', 'a:hlinkMouseOver', 'a:rtl', 'a:extLst'))
```

### `get_or_add_hlinkClick`

```python
get_or_add_hlinkClick: Callable[[], CT_Hyperlink]
```

### `get_or_add_latin`

```python
get_or_add_latin: Callable[[], CT_TextFont]
```

### `hlinkClick`

```python
hlinkClick: CT_Hyperlink | None = ZeroOrOne('a:hlinkClick', successors=('a:hlinkMouseOver', 'a:rtl', 'a:extLst'))
```

### `i`

```python
i: bool | None = OptionalAttribute('i', XsdBoolean)
```

### `lang`

```python
lang: MSO_LANGUAGE_ID | None = OptionalAttribute('lang', MSO_LANGUAGE_ID)
```

### `latin`

```python
latin: CT_TextFont | None = ZeroOrOne('a:latin', successors=('a:ea', 'a:cs', 'a:sym', 'a:hlinkClick', 'a:hlinkMouseOver', 'a:rtl', 'a:extLst'))
```

### `sz`

```python
sz: int | None = OptionalAttribute('sz', ST_TextFontSize)
```

### `u`

```python
u: MSO_TEXT_UNDERLINE_TYPE | None = OptionalAttribute('u', MSO_TEXT_UNDERLINE_TYPE)
```

## `CT_TextField`

Bases: `BaseOxmlElement`

`a:fld` field element, for either a slide number or date field.

### `get_or_add_rPr`

```python
get_or_add_rPr: Callable[[], CT_TextCharacterProperties]
```

### `id`

```python
id: str = RequiredAttribute('id', XsdString)
```

### `rPr`

```python
rPr: CT_TextCharacterProperties | None = ZeroOrOne('a:rPr', successors=('a:pPr', 'a:t'))
```

### `t`

```python
t: BaseOxmlElement | None = ZeroOrOne('a:t', successors=())
```

### `text`

```python
text: str
```

The text of the `a:t` child element.

### `type`

```python
type: str | None = OptionalAttribute('type', XsdString)
```

## `CT_TextFont`

Bases: `BaseOxmlElement`

Custom element class for `a:latin`, `a:ea`, `a:cs`, and `a:sym`.

These occur as child elements of CT_TextCharacterProperties, e.g. `a:rPr`.

### `typeface`

```python
typeface: str = RequiredAttribute('typeface', ST_TextTypeface)
```

## `CT_TextLineBreak`

Bases: `BaseOxmlElement`

`a:br` line break element

### `get_or_add_rPr`

```python
get_or_add_rPr: Callable[[], CT_TextCharacterProperties]
```

### `rPr`

```python
rPr = ZeroOrOne('a:rPr', successors=())
```

### `text`

```python
text
```

Unconditionally a single vertical-tab character.

A line break element can contain no text other than the implicit line feed it
represents.

## `CT_TextListStyle`

Bases: `BaseOxmlElement`

`a:lstStyle` custom element class.

Also used for the master's `p:titleStyle`/`p:bodyStyle`/`p:otherStyle` children and the
presentation's `p:defaultTextStyle` — all are the same `CT_TextListStyle` complex type: a
sequence of `a:lvl1pPr` … `a:lvl9pPr` (plus `a:defPPr`) paragraph-property elements.

### `pPr_for_lvl`

```python
pPr_for_lvl(level: int) -> CT_TextParagraphProperties | None
```

Return the `a:lvl{level+1}pPr` child for 0-based indent `level`, or `None`.

Read-only helper for the effective-style inheritance walk.

## `CT_TextNormalAutofit`

Bases: `BaseOxmlElement`

`a:normAutofit` element specifying fit text to shape font reduction, etc.

### `fontScale`

```python
fontScale = OptionalAttribute('fontScale', ST_TextFontScalePercentOrPercentString, default=100.0)
```

### `lnSpcReduction`

```python
lnSpcReduction = OptionalAttribute('lnSpcReduction', ST_TextLineSpaceReductionPercentOrPercentString, default=0.0)
```

## `CT_TextParagraph`

Bases: `BaseOxmlElement`

`a:p` custom element class

### `add_br`

```python
add_br() -> CT_TextLineBreak
```

Return a newly appended `a:br` element.

### `add_fld`

```python
add_fld(id_str: str, field_type: str, cached_text: str) -> CT_TextField
```

Append an `a:fld` with `id_str`/`field_type` and cached `a:t` text (paper-pptx).

Content children (`a:r`/`a:br`/`a:fld`) precede `a:endParaRPr` per the schema; the
new field is inserted accordingly.

### `add_r`

```python
add_r(text: str | None = None) -> CT_RegularTextRun
```

Return a newly appended `a:r` element.

### `append_text`

```python
append_text(text: str)
```

Append `a:r` and `a:br` elements to `p` based on `text`.

        Any `
` or `` (vertical-tab) characters in `text` delimit `a:r` (run) elements and
        themselves are translated to `a:br` (line-break) elements. The vertical-tab character
        appears in clipboard text from PowerPoint at "soft" line-breaks (new-line, but not new
        paragraph).

### `br`

```python
br = ZeroOrMore('a:br', successors=('a:endParaRPr',))
```

### `content_children`

```python
content_children: tuple[CT_RegularTextRun | CT_TextLineBreak | CT_TextField, ...]
```

Sequence containing text-container child elements of this `a:p` element.

These include `a:r`, `a:br`, and `a:fld`.

### `endParaRPr`

```python
endParaRPr: CT_TextCharacterProperties | None = ZeroOrOne('a:endParaRPr', successors=())
```

### `get_or_add_endParaRPr`

```python
get_or_add_endParaRPr: Callable[[], CT_TextCharacterProperties]
```

### `get_or_add_pPr`

```python
get_or_add_pPr: Callable[[], CT_TextParagraphProperties]
```

### `pPr`

```python
pPr: CT_TextParagraphProperties | None = ZeroOrOne('a:pPr', successors=('a:r', 'a:br', 'a:fld', 'a:endParaRPr'))
```

### `r`

```python
r = ZeroOrMore('a:r', successors=('a:endParaRPr',))
```

### `r_lst`

```python
r_lst: list[CT_RegularTextRun]
```

### `text`

```python
text: str
```

str text contained in this paragraph.

## `CT_TextParagraphProperties`

Bases: `BaseOxmlElement`

`a:pPr` custom element class.

### `algn`

```python
algn: PP_PARAGRAPH_ALIGNMENT | None = OptionalAttribute('algn', PP_PARAGRAPH_ALIGNMENT)
```

### `buAutoNum`

```python
buAutoNum: CT_TextAutonumberBullet | None
```

### `buChar`

```python
buChar: CT_TextCharBullet | None
```

### `buFont`

```python
buFont: CT_TextFont | None = ZeroOrOne('a:buFont', successors=_tag_seq[10:])
```

### `buNone`

```python
buNone: BaseOxmlElement | None
```

### `buSzPct`

```python
buSzPct: CT_TextBulletSizePercent | None = ZeroOrOne('a:buSzPct', successors=_tag_seq[7:])
```

### `defRPr`

```python
defRPr: CT_TextCharacterProperties | None = ZeroOrOne('a:defRPr', successors=_tag_seq[16:])
```

### `eg_bullet`

```python
eg_bullet = ZeroOrOneChoice((Choice('a:buNone'), Choice('a:buAutoNum'), Choice('a:buChar')), successors=_tag_seq[13:])
```

### `get_or_add_buFont`

```python
get_or_add_buFont: Callable[[], CT_TextFont]
```

### `get_or_add_buSzPct`

```python
get_or_add_buSzPct: Callable[[], CT_TextBulletSizePercent]
```

### `get_or_add_defRPr`

```python
get_or_add_defRPr: Callable[[], CT_TextCharacterProperties]
```

### `get_or_change_to_buAutoNum`

```python
get_or_change_to_buAutoNum: Callable[[], CT_TextAutonumberBullet]
```

### `get_or_change_to_buChar`

```python
get_or_change_to_buChar: Callable[[], CT_TextCharBullet]
```

### `get_or_change_to_buNone`

```python
get_or_change_to_buNone: Callable[[], BaseOxmlElement]
```

### `indent`

```python
indent: Length | None = OptionalAttribute('indent', ST_TextIndent)
```

### `line_spacing`

```python
line_spacing: float | Length | None
```

The spacing between baselines of successive lines in this paragraph.

A float value indicates a number of lines. A `Length` value indicates a fixed spacing.
Value is contained in `./a:lnSpc/a:spcPts/@val` or `./a:lnSpc/a:spcPct/@val`. Value is
`None` if no element is present.

### `lnSpc`

```python
lnSpc: CT_TextSpacing | None = ZeroOrOne('a:lnSpc', successors=_tag_seq[1:])
```

### `lvl`

```python
lvl: int = OptionalAttribute('lvl', ST_TextIndentLevelType, default=0)
```

### `marL`

```python
marL: Length | None = OptionalAttribute('marL', ST_TextMargin)
```

### `space_after`

```python
space_after: Length | None
```

The EMU equivalent of the centipoints value in `./a:spcAft/a:spcPts/@val`.

### `space_before`

```python
space_before
```

The EMU equivalent of the centipoints value in `./a:spcBef/a:spcPts/@val`.

### `spcAft`

```python
spcAft: CT_TextSpacing | None = ZeroOrOne('a:spcAft', successors=_tag_seq[3:])
```

### `spcBef`

```python
spcBef: CT_TextSpacing | None = ZeroOrOne('a:spcBef', successors=_tag_seq[2:])
```

## `CT_TextSpacing`

Bases: `BaseOxmlElement`

Used for `a:lnSpc`, `a:spcBef`, and `a:spcAft` elements.

### `get_or_add_spcPct`

```python
get_or_add_spcPct: Callable[[], CT_TextSpacingPercent]
```

### `get_or_add_spcPts`

```python
get_or_add_spcPts: Callable[[], CT_TextSpacingPoint]
```

### `set_spcPct`

```python
set_spcPct(value: float)
```

Set spacing to `value` lines, e.g. 1.75 lines.

A ./a:spcPts child is removed if present.

### `set_spcPts`

```python
set_spcPts(value: Length)
```

Set spacing to `value` points. A ./a:spcPct child is removed if present.

### `spcPct`

```python
spcPct: CT_TextSpacingPercent | None = ZeroOrOne('a:spcPct')
```

### `spcPts`

```python
spcPts: CT_TextSpacingPoint | None = ZeroOrOne('a:spcPts')
```

## `CT_TextSpacingPercent`

Bases: `BaseOxmlElement`

`a:spcPct` element, specifying spacing in thousandths of a percent in its `val` attribute.

### `val`

```python
val: float = RequiredAttribute('val', ST_TextSpacingPercentOrPercentString)
```

## `CT_TextSpacingPoint`

Bases: `BaseOxmlElement`

`a:spcPts` element, specifying spacing in centipoints in its `val` attribute.

### `val`

```python
val: Length = RequiredAttribute('val', ST_TextSpacingPoint)
```
