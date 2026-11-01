<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.coreprops`

Custom element classes for core properties-related XML elements.

## `CT_CoreProperties`

Bases: `BaseOxmlElement`

`<cp:coreProperties>` element, the root element of the Core Properties part.

Stored as `/docProps/core.xml`. Implements many of the Dublin Core document metadata
elements. String elements resolve to an empty string ("") if the element is not
present in the XML. String elements are limited in length to 255 unicode characters.

### `author_text`

```python
author_text: str
```

The text in the `dc:creator` child element.

### `category`

```python
category = ZeroOrOne('cp:category', successors=())
```

### `category_text`

```python
category_text: str
```

### `comments_text`

```python
comments_text: str
```

### `contentStatus`

```python
contentStatus = ZeroOrOne('cp:contentStatus', successors=())
```

### `contentStatus_text`

```python
contentStatus_text: str
```

### `created`

```python
created = ZeroOrOne('dcterms:created', successors=())
```

### `created_datetime`

```python
created_datetime: dt.datetime | None
```

### `creator`

```python
creator = ZeroOrOne('dc:creator', successors=())
```

### `description`

```python
description = ZeroOrOne('dc:description', successors=())
```

### `get_or_add_revision`

```python
get_or_add_revision: Callable[[], etree_Element]
```

### `identifier`

```python
identifier = ZeroOrOne('dc:identifier', successors=())
```

### `identifier_text`

```python
identifier_text: str
```

### `keywords`

```python
keywords = ZeroOrOne('cp:keywords', successors=())
```

### `keywords_text`

```python
keywords_text: str
```

### `language`

```python
language = ZeroOrOne('dc:language', successors=())
```

### `language_text`

```python
language_text: str
```

### `lastModifiedBy`

```python
lastModifiedBy = ZeroOrOne('cp:lastModifiedBy', successors=())
```

### `lastModifiedBy_text`

```python
lastModifiedBy_text: str
```

### `lastPrinted`

```python
lastPrinted = ZeroOrOne('cp:lastPrinted', successors=())
```

### `lastPrinted_datetime`

```python
lastPrinted_datetime: dt.datetime | None
```

### `modified`

```python
modified = ZeroOrOne('dcterms:modified', successors=())
```

### `modified_datetime`

```python
modified_datetime: dt.datetime | None
```

### `new`

```python
new() -> CT_CoreProperties
```

Return a new `<cp:coreProperties>` element.

### `revision`

```python
revision: etree_Element | None = ZeroOrOne('cp:revision', successors=())
```

### `revision_number`

```python
revision_number: int
```

Integer value of revision property.

### `subject`

```python
subject = ZeroOrOne('dc:subject', successors=())
```

### `subject_text`

```python
subject_text: str
```

### `title`

```python
title = ZeroOrOne('dc:title', successors=())
```

### `title_text`

```python
title_text: str
```

### `version`

```python
version = ZeroOrOne('cp:version', successors=())
```

### `version_text`

```python
version_text: str
```
