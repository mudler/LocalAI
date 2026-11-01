<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml`

Initializes lxml parser, particularly the custom element classes.

## `element_class_lookup`

```python
element_class_lookup = etree.ElementNamespaceClassLookup()
```

## `oxml_parser`

```python
oxml_parser = etree.XMLParser(remove_blank_text=True, resolve_entities=False)
```

## `parse_from_template`

```python
parse_from_template(template_file_name: str)
```

Return an element loaded from the XML in the template file identified by `template_name`.

## `parse_xml`

```python
parse_xml(xml: str | bytes)
```

Return root lxml element obtained by parsing XML character string in `xml`.

## `register_element_cls`

```python
register_element_cls(nsptagname: str, cls: Type[BaseOxmlElement])
```

Register `cls` to be constructed when oxml parser encounters element having `nsptag_name`.

`nsptag_name` is a string of the form `nspfx:tagroot`, e.g. `"w:document"`.
