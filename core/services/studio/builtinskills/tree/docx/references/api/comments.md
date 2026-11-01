<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.comments`

Collection providing access to comments added to this document.

## `Comment`

```python
Comment(comment_elm: CT_Comment, comments_part: CommentsPart)
```

Bases: `BlockItemContainer`

Proxy for a single comment in the document.

Reads author, initials, date and body content, and adds paragraphs or tables. Every
mutation revalidates that the comment is still live first, so a proxy whose part was
removed or replaced refuses instead of writing into a detached tree.

### `add_paragraph`

```python
add_paragraph(text: str = '', style: str | ParagraphStyle | None = None) -> Paragraph
```

Append a paragraph to this comment's body and return it.

Re-keys the comment's `commentEx`, its replies' `paraIdParent`, and its `commentsIds` row
onto the new last paragraph, so appending rewrites thread and resolution state. Refuses a
protected document, and a proxy whose part was removed or replaced.

### `add_table`

```python
add_table(rows: int, cols: int, width: Length) -> Table
```

Append a table to this comment's body and return it.

Re-keys thread and resolution state onto the new last paragraph, as `add_paragraph` does.
Refuses a protected document, and a proxy whose part was removed or replaced.

### `author`

```python
author: str
```

Read/write. The recorded author of this comment.

This field is required but can be set to the empty string.

### `comment_id`

```python
comment_id: int
```

The unique identifier of this comment.

### `initials`

```python
initials: str | None
```

Read/write. The recorded initials of the comment author.

This attribute is optional in the XML, returns `None` if not set. Assigning `None` removes
any existing initials from the XML.

### `text`

```python
text: str
```

The text content of this comment as a string.

Only content in paragraphs is included and of course all emphasis and styling is stripped.

Paragraph boundaries are indicated with a newline (`"\\n"`)

### `timestamp`

```python
timestamp: dt.datetime | None
```

The date and time this comment was authored.

This attribute is optional in the XML, returns `None` if not set.

## `Comments`

```python
Comments(comments_elm: CT_Comments, comments_part: CommentsPart)
```

Collection containing the comments added to this document.

### `add_comment`

```python
add_comment(text: str = '', author: str = '', initials: str | None = '') -> Comment
```

Add a new comment to the document and return it.

Assigns a unique id and writes the modern identity parts (`commentsIds`,
`commentsExtensible`, a `w14:paraId`) that Word needs to thread replies. Refuses a
protected document, a collection whose part was removed or replaced, and an ambiguous
comments relationship.

### `get`

```python
get(comment_id: int) -> Comment | None
```

Return the comment identified by `comment_id`, or `None` if not found.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.

## `rollback_xml_on_error`

```python
rollback_xml_on_error(root: '_Element') -> Generator[None, None, None]
```

Restore one live XML tree after an error in a local compound edit.
