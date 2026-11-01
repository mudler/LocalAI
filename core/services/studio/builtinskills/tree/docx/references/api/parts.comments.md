<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.parts.comments`

Contains comments added to the document.

## `CommentsPart`

```python
CommentsPart(partname: PackURI, content_type: str, element: CT_Comments, package: Package)
```

Bases: `StoryPart`

Container part for comments added to the document.

### `comments`

```python
comments: Comments
```

A `Comments` proxy object for the `w:comments` root element of this part.

### `default`

```python
default(package: Package) -> Self
```

A newly created comments part, containing a default empty `w:comments` element.
