<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.numbering`

Numbering enumeration, application, and minimal authoring (paper-docx).

## `NumberedParagraph`

```python
NumberedParagraph(story: str, index: int, num_id: int, level: int, text: str, table_cell: 'Optional[Tuple[int, int, int]]' = None) -> None
```

### `index`

```python
index: int
```

### `level`

```python
level: int
```

### `num_id`

```python
num_id: int
```

### `story`

```python
story: str
```

### `table_cell`

```python
table_cell: 'Optional[Tuple[int, int, int]]' = None
```

### `text`

```python
text: str
```

### `to_dict`

```python
to_dict() -> dict
```

## `NumberingDefinition`

```python
NumberingDefinition(num_id: int, abstract_num_id: int, levels: Tuple[NumberingLevel, ...]) -> None
```

### `abstract_num_id`

```python
abstract_num_id: int
```

### `levels`

```python
levels: Tuple[NumberingLevel, ...]
```

### `num_id`

```python
num_id: int
```

### `to_dict`

```python
to_dict() -> dict
```

## `NumberingLevel`

```python
NumberingLevel(level: int, num_fmt: Optional[str], lvl_text: Optional[str]) -> None
```

### `level`

```python
level: int
```

### `lvl_text`

```python
lvl_text: Optional[str]
```

### `num_fmt`

```python
num_fmt: Optional[str]
```

### `to_dict`

```python
to_dict() -> dict
```

## `NumberingReport`

```python
NumberingReport(definitions: Tuple[NumberingDefinition, ...], numbered_paragraphs: Tuple[NumberedParagraph, ...]) -> None
```

### `definitions`

```python
definitions: Tuple[NumberingDefinition, ...]
```

### `numbered_paragraphs`

```python
numbered_paragraphs: Tuple[NumberedParagraph, ...]
```

### `to_dict`

```python
to_dict() -> dict
```

## `apply_list_style`

```python
apply_list_style(paragraph: 'Paragraph', style_name: str) -> None
```

Apply the existing paragraph style `style_name` to `paragraph`.

Refuses a protected document, a style the document does not define, and a style whose
numbering binding does not resolve.

## `apply_numbering`

```python
apply_numbering(paragraph: 'Paragraph', *, num_id: int, level: int = 0) -> None
```

Give `paragraph` the existing numbering definition `num_id` at `level`.

Writes `w:numPr` into the paragraph's `w:pPr`. This never fabricates a definition; create
one deliberately with `ensure_bullet_definition` or `ensure_decimal_definition`. Refuses a
protected document, and a numId or level the document does not define.

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `ensure_bullet_definition`

```python
ensure_bullet_definition(document: 'Document') -> int
```

The numId of a canonical bullet-list definition, creating it when absent.

Creates `/word/numbering.xml` and its relationship on first use. Reuse requires an exactly
canonical definition, so a hand-edited one of the same name is left alone and a new
definition is appended. Refuses a protected document.

## `ensure_decimal_definition`

```python
ensure_decimal_definition(document: 'Document') -> int
```

The numId of a canonical decimal-list definition, creating it when absent.

Same creation and exact-shape reuse rules as `ensure_bullet_definition`. Refuses a
protected document.

## `list_numbering`

```python
list_numbering(document: 'Document') -> NumberingReport
```

Every numbering definition and every numbered paragraph in `document`.

Walks all story parts and reports text in the "current" view. Refuses when a paragraph
resolves to a definition the document does not define, or to a level that definition
omits.

## `restart_numbering`

```python
restart_numbering(document: 'Document', *, num_id: int) -> int
```

A NEW numId continuing `num_id`'s formatting but restarting the count at 1.

Paragraphs are not re-pointed; re-point them yourself with `apply_numbering`. Refuses a
protected document, an unknown numId, a malformed `w:numId`, and a definition carrying
`w:lvlOverride`.
