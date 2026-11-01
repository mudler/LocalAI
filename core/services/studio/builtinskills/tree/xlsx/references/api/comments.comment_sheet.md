<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.comments.comment_sheet`

## `CommentRecord`

```python
CommentRecord(ref = '', authorId = 0, guid = None, shapeId = 0, text = None, commentPr = None, author = None, height = 79, width = 144)
```

Bases: `Serialisable`

### `author`

```python
author = author
```

### `authorId`

```python
authorId = authorId
```

### `commentPr`

```python
commentPr = commentPr
```

### `content`

```python
content
```

Remove all inline formatting and stuff

### `from_cell`

```python
from_cell(cell)
```

Class method to convert cell comment

### `guid`

```python
guid = guid
```

### `height`

```python
height = height
```

### `ref`

```python
ref = ref
```

### `shapeId`

```python
shapeId = shapeId
```

### `tagname`

```python
tagname = 'comment'
```

### `text`

```python
text = text
```

### `width`

```python
width = width
```

## `CommentSheet`

```python
CommentSheet(authors = None, commentList = None, extLst = None)
```

Bases: `Serialisable`

### `authors`

```python
authors = authors
```

### `commentList`

```python
commentList = commentList
```

### `comments`

```python
comments
```

Return a dictionary of comments keyed by coord

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `from_comments`

```python
from_comments(comments)
```

Create a comment sheet from a list of comments for a particular worksheet

### `mime_type`

```python
mime_type = 'application/vnd.openxmlformats-officedocument.spreadsheetml.comments+xml'
```

### `path`

```python
path
```

Return path within the archive

### `tagname`

```python
tagname = 'comments'
```

### `to_tree`

```python
to_tree()
```

### `write_shapes`

```python
write_shapes(vml = None)
```

Create the VML for comments

## `Properties`

```python
Properties(locked = None, defaultSize = None, _print = None, disabled = None, uiObject = None, autoFill = None, autoLine = None, altText = None, textHAlign = None, textVAlign = None, lockText = None, justLastX = None, autoScale = None, rowHidden = None, colHidden = None, anchor = None)
```

Bases: `Serialisable`

### `altText`

```python
altText = altText
```

### `anchor`

```python
anchor = anchor
```

### `autoFill`

```python
autoFill = autoFill
```

### `autoLine`

```python
autoLine = autoLine
```

### `autoScale`

```python
autoScale = autoScale
```

### `colHidden`

```python
colHidden = colHidden
```

### `defaultSize`

```python
defaultSize = defaultSize
```

### `disabled`

```python
disabled = disabled
```

### `justLastX`

```python
justLastX = justLastX
```

### `lockText`

```python
lockText = lockText
```

### `locked`

```python
locked = locked
```

### `rowHidden`

```python
rowHidden = rowHidden
```

### `textHAlign`

```python
textHAlign = textHAlign
```

### `textVAlign`

```python
textVAlign = textVAlign
```

### `uiObject`

```python
uiObject = uiObject
```
