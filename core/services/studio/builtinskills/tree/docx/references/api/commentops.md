<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.commentops`

Comment thread operations: anchored text, replies, resolution.

## `COMMENTS_EXTENDED_CONTENT_TYPE`

```python
COMMENTS_EXTENDED_CONTENT_TYPE = 'application/vnd.openxmlformats-officedocument.wordprocessingml.commentsExtended+xml'
```

## `COMMENTS_EXTENDED_RELATIONSHIP_TYPE`

```python
COMMENTS_EXTENDED_RELATIONSHIP_TYPE = 'http://schemas.microsoft.com/office/2011/relationships/commentsExtended'
```

## `COMMENTS_EXTENSIBLE_CONTENT_TYPE`

```python
COMMENTS_EXTENSIBLE_CONTENT_TYPE = 'application/vnd.openxmlformats-officedocument.wordprocessingml.commentsExtensible+xml'
```

## `COMMENTS_EXTENSIBLE_RELATIONSHIP_TYPE`

```python
COMMENTS_EXTENSIBLE_RELATIONSHIP_TYPE = 'http://schemas.microsoft.com/office/2018/08/relationships/commentsExtensible'
```

## `COMMENTS_IDS_CONTENT_TYPE`

```python
COMMENTS_IDS_CONTENT_TYPE = 'application/vnd.openxmlformats-officedocument.wordprocessingml.commentsIds+xml'
```

## `COMMENTS_IDS_RELATIONSHIP_TYPE`

```python
COMMENTS_IDS_RELATIONSHIP_TYPE = 'http://schemas.microsoft.com/office/2016/09/relationships/commentsIds'
```

## `DEL_TEXT`

```python
DEL_TEXT = qn('w:delText')
```

## `INSTR_TEXT`

```python
INSTR_TEXT = qn('w:instrText')
```

## `anchored_text`

```python
anchored_text(document: 'Document', comment: 'Comment') -> str
```

The document text `comment` is anchored to, taken from its range marks.

Refuses a stale or foreign comment, and a comment whose range marks are missing or
crossed.

## `check_install`

```python
check_install() -> None
```

Refuse when paper-docx and python-docx are both installed.

## `comment_thread`

```python
comment_thread(document: 'Document') -> Tuple[dict, ...]
```

Every comment with its thread state: id, author, text, resolved, parent id, and anchored
text where available.

Use it to read a whole discussion in one pass rather than walking replies. Refuses a
stale comment and a malformed `commentsExtended` part.

## `delete_comment`

```python
delete_comment(document: 'Document', comment: 'Comment') -> None
```

Delete `comment` and its replies, removing their range marks from the text.

Refuses a protected document, a stale or foreign comment, and a comments part that is
missing or ambiguous.

## `is_direct_run_child`

```python
is_direct_run_child(element: '_Element') -> bool
```

Whether ``element`` is a direct child of a ``w:r`` element.

## `is_resolved`

```python
is_resolved(document: 'Document', comment: 'Comment') -> bool
```

Whether `comment`'s thread is marked resolved.

Reads `commentsExtended`, where Word keeps resolution state. Refuses a stale or foreign
comment, and a malformed `commentsExtended` part.

## `parent_of`

```python
parent_of(document: 'Document', comment: 'Comment') -> Optional[int]
```

The comment `comment` replies to, or None when it starts a thread.

Refuses a stale or foreign comment, and a malformed `commentsExtended` part.

## `project_run_child`

```python
project_run_child(element: '_Element') -> RunChildProjection
```

Return the conservative text projection for a direct ``w:r`` child.

## `reply`

```python
reply(document: 'Document', comment: 'Comment', text: str, *, author: str, initials: Optional[str] = None, date: Optional[dt.datetime] = None) -> 'Comment'
```

Add a threaded reply to `comment` and return the new `Comment`.

The reply gets its own comment-range start, range end, and reference around the same
selected text. Its `w15:paraIdParent` names the parent's final comment-body paragraph; the
reply does not reuse the parent's marker id. Creates `commentsExtended` on first use, since
Word keeps threading outside `w:comment`. Refuses a protected document, a stale or foreign
comment, and a comments part that is missing or ambiguous.

## `require_comment_owner`

```python
require_comment_owner(document: 'Document', comment: 'Comment', *, argument: str = 'comment') -> None
```

Refuse a comment proxy from another part and detect detached proxies.

## `resolve`

```python
resolve(document: 'Document', comment: 'Comment', *, resolved: bool = True) -> None
```

Mark `comment`'s thread resolved, or reopen it with `resolved=False`.

Word keeps resolution in `commentsExtended` rather than on the comment, so this writes
that part. Refuses a protected document, and a stale or foreign comment.

## `rollback_on_error`

```python
rollback_on_error(document: 'Document', *participants: Any) -> Generator[None, None, None]
```

Restore the live package and named mutable proxies after an error.
