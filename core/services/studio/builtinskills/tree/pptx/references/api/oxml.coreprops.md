<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.coreprops`

lxml custom element classes for core properties-related XML elements.

## `CT_CoreProperties`

Bases: `BaseOxmlElement`

`cp:coreProperties` element.

The root element of the Core Properties part stored as `/docProps/core.xml`. Implements many
of the Dublin Core document metadata elements. String elements resolve to an empty string ('')
if the element is not present in the XML. String elements are limited in length to 255 unicode
characters.

### `author_text`

```python
author_text: str
```

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
created_datetime
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
get_or_add_revision: Callable[[], _Element]
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
lastPrinted_datetime
```

### `modified`

```python
modified = ZeroOrOne('dcterms:modified', successors=())
```

### `modified_datetime`

```python
modified_datetime
```

### `new_coreProperties`

```python
new_coreProperties() -> CT_CoreProperties
```

Return a new `cp:coreProperties` element

### `revision`

```python
revision: _Element | None = ZeroOrOne('cp:revision', successors=())
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
