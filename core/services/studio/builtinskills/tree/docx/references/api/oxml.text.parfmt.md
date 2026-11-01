<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.text.parfmt`

Custom element classes related to paragraph properties (CT_PPr).

## `CT_Ind`

Bases: `BaseOxmlElement`

``<w:ind>`` element, specifying paragraph indentation.

### `firstLine`

```python
firstLine: Length | None = OptionalAttribute('w:firstLine', ST_TwipsMeasure)
```

### `hanging`

```python
hanging: Length | None = OptionalAttribute('w:hanging', ST_TwipsMeasure)
```

### `left`

```python
left: Length | None = OptionalAttribute('w:left', ST_SignedTwipsMeasure)
```

### `right`

```python
right: Length | None = OptionalAttribute('w:right', ST_SignedTwipsMeasure)
```

## `CT_Jc`

Bases: `BaseOxmlElement`

``<w:jc>`` element, specifying paragraph justification.

### `val`

```python
val: WD_ALIGN_PARAGRAPH = RequiredAttribute('w:val', WD_ALIGN_PARAGRAPH)
```

## `CT_PPr`

Bases: `BaseOxmlElement`

``<w:pPr>`` element, containing the properties for a paragraph.

### `first_line_indent`

```python
first_line_indent: Length | None
```

A `Length` value calculated from the values of `w:ind/@w:firstLine` and
`w:ind/@w:hanging`.

Returns `None` if the `w:ind` child is not present.

### `get_or_add_ind`

```python
get_or_add_ind: Callable[[], CT_Ind]
```

### `get_or_add_pStyle`

```python
get_or_add_pStyle: Callable[[], CT_String]
```

### `get_or_add_sectPr`

```python
get_or_add_sectPr: Callable[[], CT_SectPr]
```

### `ind`

```python
ind: CT_Ind | None = ZeroOrOne('w:ind', successors=_tag_seq[23:])
```

### `ind_left`

```python
ind_left: Length | None
```

The value of `w:ind/@w:left` or `None` if not present.

### `ind_right`

```python
ind_right: Length | None
```

The value of `w:ind/@w:right` or `None` if not present.

### `jc`

```python
jc = ZeroOrOne('w:jc', successors=_tag_seq[27:])
```

### `jc_val`

```python
jc_val: WD_ALIGN_PARAGRAPH | None
```

Value of the `<w:jc>` child element or `None` if not present.

### `keepLines`

```python
keepLines = ZeroOrOne('w:keepLines', successors=_tag_seq[3:])
```

### `keepLines_val`

```python
keepLines_val
```

The value of `keepLines/@val` or `None` if not present.

### `keepNext`

```python
keepNext = ZeroOrOne('w:keepNext', successors=_tag_seq[2:])
```

### `keepNext_val`

```python
keepNext_val
```

The value of `keepNext/@val` or `None` if not present.

### `numPr`

```python
numPr = ZeroOrOne('w:numPr', successors=_tag_seq[7:])
```

### `outlineLvl`

```python
outlineLvl: CT_DecimalNumber = ZeroOrOne('w:outlineLvl', successors=_tag_seq[31:])
```

### `pStyle`

```python
pStyle: CT_String | None = ZeroOrOne('w:pStyle', successors=_tag_seq[1:])
```

### `pageBreakBefore`

```python
pageBreakBefore = ZeroOrOne('w:pageBreakBefore', successors=_tag_seq[4:])
```

### `pageBreakBefore_val`

```python
pageBreakBefore_val
```

The value of `pageBreakBefore/@val` or `None` if not present.

### `sectPr`

```python
sectPr = ZeroOrOne('w:sectPr', successors=_tag_seq[35:])
```

### `spacing`

```python
spacing = ZeroOrOne('w:spacing', successors=_tag_seq[22:])
```

### `spacing_after`

```python
spacing_after
```

The value of `w:spacing/@w:after` or `None` if not present.

### `spacing_before`

```python
spacing_before
```

The value of `w:spacing/@w:before` or `None` if not present.

### `spacing_line`

```python
spacing_line
```

The value of `w:spacing/@w:line` or `None` if not present.

### `spacing_lineRule`

```python
spacing_lineRule
```

The value of `w:spacing/@w:lineRule` as a member of the `WdLineSpacing`
enumeration.

Only the `MULTIPLE`, `EXACTLY`, and `AT_LEAST` members are used. It is the
responsibility of the client to calculate the use of `SINGLE`, `DOUBLE`, and
`MULTIPLE` based on the value of `w:spacing/@w:line` if that behavior is
desired.

### `style`

```python
style: str | None
```

String contained in `./w:pStyle/@val`, or None if child is not present.

### `tabs`

```python
tabs = ZeroOrOne('w:tabs', successors=_tag_seq[11:])
```

### `widowControl`

```python
widowControl = ZeroOrOne('w:widowControl', successors=_tag_seq[6:])
```

### `widowControl_val`

```python
widowControl_val
```

The value of `widowControl/@val` or `None` if not present.

## `CT_Spacing`

Bases: `BaseOxmlElement`

``<w:spacing>`` element, specifying paragraph spacing attributes such as space
before and line spacing.

### `after`

```python
after = OptionalAttribute('w:after', ST_TwipsMeasure)
```

### `before`

```python
before = OptionalAttribute('w:before', ST_TwipsMeasure)
```

### `line`

```python
line = OptionalAttribute('w:line', ST_SignedTwipsMeasure)
```

### `lineRule`

```python
lineRule = OptionalAttribute('w:lineRule', WD_LINE_SPACING)
```

## `CT_TabStop`

Bases: `BaseOxmlElement`

`<w:tab>` element, representing an individual tab stop.

Overloaded to use for a tab-character in a run, which also uses the w:tab tag but
only needs a __str__ method.

### `leader`

```python
leader: WD_TAB_LEADER | None = OptionalAttribute('w:leader', WD_TAB_LEADER, default=WD_TAB_LEADER.SPACES)
```

### `pos`

```python
pos: Length = RequiredAttribute('w:pos', ST_SignedTwipsMeasure)
```

### `val`

```python
val: WD_TAB_ALIGNMENT = RequiredAttribute('w:val', WD_TAB_ALIGNMENT)
```

## `CT_TabStops`

Bases: `BaseOxmlElement`

``<w:tabs>`` element, container for a sorted sequence of tab stops.

### `insert_tab_in_order`

```python
insert_tab_in_order(pos, align, leader)
```

Insert a newly created `w:tab` child element in `pos` order.

### `tab`

```python
tab = OneOrMore('w:tab', successors=())
```
