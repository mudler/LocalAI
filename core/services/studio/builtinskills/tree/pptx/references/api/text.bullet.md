<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.text.bullet`

BulletFormat proxy, providing bullet and numbering control for a paragraph (paper-pptx).

## `BulletFormat`

```python
BulletFormat(p: CT_TextParagraph, part: CT_TextParagraph = None)
```

Bases: `object`

Bullet and numbering state of a single paragraph.

Read properties report this paragraph's **local** `a:pPr` state only: `None` means "nothing
set here", in which case rendering inherits from the placeholder/list-style chain (use the
effective-style inspection API to see what actually renders).

Wrap a paragraph's bullet properties.

Passing `part` enables the attachment check and the rollback boundary that the setters use.

### `char`

```python
char: str | None
```

The bullet character (`a:buChar/@char`), `None` unless a character bullet is set.

### `font_name`

```python
font_name: str | None
```

Bullet-specific typeface (`a:buFont/@typeface`), or `None` when not set locally.

### `number_scheme`

```python
number_scheme: str | None
```

Numbering scheme token (`a:buAutoNum/@type`), e.g. "arabicPeriod", or `None`.

### `set_character`

```python
set_character(char: str = '•', *, font_name: str | None = None, size_percent: float | None = None, left_margin: Length | None = DEFAULT_BULLET_LEFT_MARGIN, hanging_indent: Length | None = DEFAULT_BULLET_HANGING_INDENT) -> None
```

Give this paragraph a real character bullet (`a:buChar`).

`left_margin`/`hanging_indent` write `marL`/`indent` so the bullet hangs correctly;
pass `None` for either to leave the existing paragraph attribute untouched.
`font_name` sets a bullet-specific typeface (`a:buFont`); `size_percent` scales the
bullet relative to the text size (fraction, 0.25–4.0).

### `set_none`

```python
set_none() -> None
```

Set an explicit "no bullet" (`a:buNone`), overriding any inherited bullet.

Margins, bullet font, and bullet size attributes are left untouched.

### `set_numbered`

```python
set_numbered(scheme: str = 'arabicPeriod', *, start_at: int = 1, font_name: str | None = None, size_percent: float | None = None, left_margin: Length | None = DEFAULT_BULLET_LEFT_MARGIN, hanging_indent: Length | None = DEFAULT_BULLET_HANGING_INDENT) -> None
```

Give this paragraph PowerPoint automatic numbering (`a:buAutoNum`).

`scheme` is an ECMA-376 auto-number token like "arabicPeriod" or "romanUcParenR";
an unknown token raises `ValueError`. Numbering restarts at `start_at`.

### `size_percent`

```python
size_percent: float | None
```

Bullet size as a fraction of the text size (`a:buSzPct`), e.g. 0.75, or `None`.

### `start_at`

```python
start_at: int | None
```

First number of an auto-numbered sequence, `None` unless numbering is set.

### `type`

```python
type: PP_BULLET_TYPE | None
```

Kind of bullet explicitly set on this paragraph, `None` when nothing local is set.

## `DEFAULT_BULLET_HANGING_INDENT`

```python
DEFAULT_BULLET_HANGING_INDENT = Emu(171450)
```

## `DEFAULT_BULLET_LEFT_MARGIN`

```python
DEFAULT_BULLET_LEFT_MARGIN = Emu(342900)
```
