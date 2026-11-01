<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.blkcntnr`

Block item container, used by body, cell, header, etc.

## `BlockItemContainer`

```python
BlockItemContainer(element: BlockItemElement, parent: t.ProvidesStoryPart)
```

Bases: `StoryChild`

Base class for proxy objects that can contain block items.

These containers include _Body, _Cell, header, footer, footnote, endnote, comment,
and text box objects. Provides the shared functionality to add a block item like a
paragraph or table.

### `add_paragraph`

```python
add_paragraph(text: str = '', style: str | ParagraphStyle | None = None) -> Paragraph
```

Return paragraph newly added to the end of the content in this container.

The paragraph has `text` in a single run if present, and is given paragraph
style `style`.

If `style` is `None`, no paragraph style is applied, which has the same effect
as applying the 'Normal' style.

### `add_table`

```python
add_table(rows: int, cols: int, width: Length) -> Table
```

Return table of `width` having `rows` rows and `cols` columns.

The table is appended appended at the end of the content in this container.

`width` is evenly distributed between the table columns.

### `iter_inner_content`

```python
iter_inner_content() -> Iterator[Paragraph | Table]
```

Generate each `Paragraph` or `Table` in this container in document order.

### `paragraphs`

```python
paragraphs
```

A list containing the paragraphs in this container, in document order.

Read-only.

### `tables`

```python
tables
```

A list containing the tables in this container, in document order.

Read-only.

## `BlockItemElement`

```python
BlockItemElement: TypeAlias = 'CT_Body | CT_Comment | CT_HdrFtr | CT_Tc'
```
