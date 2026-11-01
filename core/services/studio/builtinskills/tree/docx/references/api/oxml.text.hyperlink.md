<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.text.hyperlink`

Custom element classes related to hyperlinks (CT_Hyperlink).

## `CT_Hyperlink`

Bases: `BaseOxmlElement`

`<w:hyperlink>` element, containing the text and address for a hyperlink.

### `anchor`

```python
anchor: str | None = OptionalAttribute('w:anchor', ST_String)
```

### `history`

```python
history: bool = OptionalAttribute('w:history', ST_OnOff, default=True)
```

### `lastRenderedPageBreaks`

```python
lastRenderedPageBreaks: List[CT_LastRenderedPageBreak]
```

All `w:lastRenderedPageBreak` descendants of this hyperlink.

### `r`

```python
r = ZeroOrMore('w:r')
```

### `rId`

```python
rId: str | None = OptionalAttribute('r:id', XsdString)
```

### `r_lst`

```python
r_lst: List[CT_R]
```

### `text`

```python
text: str
```

The textual content of this hyperlink.

`CT_Hyperlink` stores the hyperlink-text as one or more `w:r` children.
