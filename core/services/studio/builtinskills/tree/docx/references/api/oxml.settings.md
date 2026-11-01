<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.settings`

Custom element classes related to document settings.

## `CT_Settings`

Bases: `BaseOxmlElement`

`w:settings` element, root element for the settings part.

### `evenAndOddHeaders`

```python
evenAndOddHeaders: CT_OnOff | None = ZeroOrOne('w:evenAndOddHeaders', successors=_tag_seq[48:])
```

### `evenAndOddHeaders_val`

```python
evenAndOddHeaders_val: bool
```

Value of `w:evenAndOddHeaders/@w:val` or `None` if not present.

### `get_or_add_evenAndOddHeaders`

```python
get_or_add_evenAndOddHeaders: Callable[[], CT_OnOff]
```
