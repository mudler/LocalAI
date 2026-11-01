<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.text.run`

Custom element classes related to text runs (CT_R).

## `CT_Br`

Bases: `BaseOxmlElement`

`<w:br>` element, indicating a line, page, or column break in a run.

### `clear`

```python
clear: str | None = OptionalAttribute('w:clear', ST_BrClear)
```

### `type`

```python
type: str | None = OptionalAttribute('w:type', ST_BrType, default='textWrapping')
```

## `CT_Cr`

Bases: `BaseOxmlElement`

`<w:cr>` element, representing a carriage-return (0x0D) character within a run.

    In Word, this represents a "soft carriage-return" in the sense that it does not end
    the paragraph the way pressing Enter (aka. Return) on the keyboard does. Here the
    text equivalent is considered to be newline ("
") since in plain-text that's the
    closest Python equivalent.

    NOTE: this complex-type name does not exist in the schema, where `w:tab` maps to
    `CT_Empty`. This name was added to give it distinguished behavior. CT_Empty is used
    for many elements.

## `CT_NoBreakHyphen`

Bases: `BaseOxmlElement`

`<w:noBreakHyphen>` element, a hyphen ineligible for a line-wrap position.

This maps to a plain-text dash ("-").

NOTE: this complex-type name does not exist in the schema, where `w:noBreakHyphen`
maps to `CT_Empty`. This name was added to give it behavior distinguished from the
many other elements represented in the schema by CT_Empty.

## `CT_PTab`

Bases: `BaseOxmlElement`

`<w:ptab>` element, representing an absolute-position tab character within a run.

This character advances the rendering position to the specified position regardless
of any tab-stops, perhaps for layout of a table-of-contents (TOC) or similar.

## `CT_R`

Bases: `BaseOxmlElement`

`<w:r>` element, containing the properties and text for a run.

### `add_br`

```python
add_br: Callable[[], CT_Br]
```

### `add_drawing`

```python
add_drawing(inline_or_anchor: CT_Inline | CT_Anchor) -> CT_Drawing
```

Return newly appended `CT_Drawing` (`w:drawing`) child element.

The `w:drawing` element has `inline_or_anchor` as its child.

### `add_t`

```python
add_t(text: str) -> CT_Text
```

Return a newly added `<w:t>` element containing `text`.

### `add_tab`

```python
add_tab: Callable[[], CT_TabStop]
```

### `br`

```python
br = ZeroOrMore('w:br')
```

### `clear_content`

```python
clear_content() -> None
```

Remove all child elements except a `w:rPr` element if present.

### `cr`

```python
cr = ZeroOrMore('w:cr')
```

### `drawing`

```python
drawing = ZeroOrMore('w:drawing')
```

### `get_or_add_rPr`

```python
get_or_add_rPr: Callable[[], CT_RPr]
```

### `inner_content_items`

```python
inner_content_items: List[str | CT_Drawing | CT_LastRenderedPageBreak]
```

Text of run, possibly punctuated by `w:lastRenderedPageBreak` elements.

### `insert_comment_range_end_and_reference_below`

```python
insert_comment_range_end_and_reference_below(comment_id: int) -> None
```

Insert a `w:commentRangeEnd` and `w:commentReference` element after this run.

The `w:commentRangeEnd` element is the immediate sibling of this `w:r` and is followed by
a `w:r` containing the `w:commentReference` element.

### `insert_comment_range_start_above`

```python
insert_comment_range_start_above(comment_id: int) -> None
```

Insert a `w:commentRangeStart` element with `comment_id` before this run.

### `lastRenderedPageBreaks`

```python
lastRenderedPageBreaks: List[CT_LastRenderedPageBreak]
```

All `w:lastRenderedPageBreaks` descendants of this run.

### `rPr`

```python
rPr: CT_RPr | None = ZeroOrOne('w:rPr')
```

### `style`

```python
style: str | None
```

String contained in `w:val` attribute of `w:rStyle` grandchild.

`None` if that element is not present.

### `t`

```python
t = ZeroOrMore('w:t')
```

### `tab`

```python
tab = ZeroOrMore('w:tab')
```

### `text`

```python
text: str
```

The textual content of this run.

Inner-content child elements like `w:tab` are translated to their text
equivalent.

## `CT_Text`

Bases: `BaseOxmlElement`

`<w:t>` element, containing a sequence of characters within a run.
