<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.action`

lxml custom element classes for text-related XML elements.

## `CT_Hyperlink`

Bases: `BaseOxmlElement`

Custom element class for <a:hlinkClick> elements.

### `action`

```python
action: str | None = OptionalAttribute('action', XsdString)
```

### `action_fields`

```python
action_fields: dict[str, str]
```

Query portion of the `ppaction://` URL as dict.

For example `{'id':'0', 'return':'true'}` in 'ppaction://customshow?id=0&return=true'.

Returns an empty dict if the URL contains no query string or if no action attribute is
present.

### `action_verb`

```python
action_verb: str | None
```

The host portion of the `ppaction://` URL contained in the action attribute.

For example 'customshow' in 'ppaction://customshow?id=0&return=true'. Returns `None` if no
action attribute is present.

### `rId`

```python
rId: str = OptionalAttribute('r:id', XsdString)
```
