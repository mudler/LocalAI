<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.comments`

Custom element classes related to document comments.

## `CT_Comment`

Bases: `BaseOxmlElement`

`w:comment` element, representing a single comment.

A comment is a so-called "story" and can contain paragraphs and tables much like a table-cell.
While probably most often used for a single sentence or phrase, a comment can contain rich
content, including multiple rich-text paragraphs, hyperlinks, images, and tables.

### `add_p`

```python
add_p: Callable[[], CT_P]
```

### `author`

```python
author: str = RequiredAttribute('w:author', ST_String)
```

### `date`

```python
date: dt.datetime | None = OptionalAttribute('w:date', ST_DateTime)
```

### `id`

```python
id: int = RequiredAttribute('w:id', ST_DecimalNumber)
```

### `initials`

```python
initials: str | None = OptionalAttribute('w:initials', ST_String)
```

### `inner_content_elements`

```python
inner_content_elements: list[CT_P | CT_Tbl]
```

Generate all `w:p` and `w:tbl` elements in this comment.

### `p`

```python
p = ZeroOrMore('w:p', successors=())
```

### `p_lst`

```python
p_lst: list[CT_P]
```

### `tbl`

```python
tbl = ZeroOrMore('w:tbl', successors=())
```

### `tbl_lst`

```python
tbl_lst: list[CT_Tbl]
```

## `CT_Comments`

Bases: `BaseOxmlElement`

`w:comments` element, the root element for the comments part.

Simply contains a collection of `w:comment` elements, each representing a single comment. Each
contained comment is identified by a unique `w:id` attribute, used to reference the comment
from the document text. The offset of the comment in this collection is arbitrary; it is
essentially a _set_ implemented as a list.

### `add_comment`

```python
add_comment() -> CT_Comment
```

Return newly added `w:comment` child of this `w:comments`.

The returned `w:comment` element is the minimum valid value, having a `w:id` value unique
within the existing comments and the required `w:author` attribute present but set to the
empty string. It's content is limited to a single run containing the necessary annotation
reference but no text. Content is added by adding runs to this first paragraph and by
adding additional paragraphs as needed.

### `comment`

```python
comment = ZeroOrMore('w:comment')
```

### `comment_lst`

```python
comment_lst: list[CT_Comment]
```

### `get_comment_by_id`

```python
get_comment_by_id(comment_id: int) -> CT_Comment | None
```

Return the `w:comment` element identified by `comment_id`, or `None` if not found.
