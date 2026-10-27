<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.opc.oxml`

Temporary stand-in for main oxml module.

## `BaseOxmlElement`

Bases: `ElementBase`

Base class for all custom element classes, to add standardized behavior to all
classes in one place.

### `xml`

```python
xml: str
```

Return XML string for this element, suitable for testing purposes.

Pretty printed for readability and without an XML declaration at the top.

## `CT_Default`

Bases: `BaseOxmlElement`

`<Default>` element that appears in `[Content_Types].xml` part.

Used to specify a default content type to be applied to any part with the specified extension.

### `content_type`

```python
content_type
```

String held in the ``ContentType`` attribute of this ``<Default>``
element.

### `extension`

```python
extension
```

String held in the ``Extension`` attribute of this ``<Default>`` element.

### `new`

```python
new(ext: str, content_type: str)
```

Return a new ``<Default>`` element with attributes set to parameter values.

## `CT_Override`

Bases: `BaseOxmlElement`

``<Override>`` element, specifying the content type to be applied for a part with
the specified partname.

### `content_type`

```python
content_type
```

String held in the ``ContentType`` attribute of this ``<Override>``
element.

### `new`

```python
new(partname, content_type)
```

Return a new ``<Override>`` element with attributes set to parameter values.

### `partname`

```python
partname
```

String held in the ``PartName`` attribute of this ``<Override>`` element.

## `CT_Relationship`

Bases: `BaseOxmlElement`

`<Relationship>` element, representing a single relationship from source to target part.

### `new`

```python
new(rId: str, reltype: str, target: str, target_mode: str = RTM.INTERNAL)
```

Return a new ``<Relationship>`` element.

### `rId`

```python
rId
```

String held in the ``Id`` attribute of this ``<Relationship>`` element.

### `reltype`

```python
reltype
```

String held in the ``Type`` attribute of this ``<Relationship>`` element.

### `target_mode`

```python
target_mode
```

String held in the ``TargetMode`` attribute of this ``<Relationship>``
element, either ``Internal`` or ``External``.

Defaults to ``Internal``.

### `target_ref`

```python
target_ref
```

String held in the ``Target`` attribute of this ``<Relationship>``
element.

## `CT_Relationships`

Bases: `BaseOxmlElement`

``<Relationships>`` element, the root element in a .rels file.

### `Relationship_lst`

```python
Relationship_lst
```

Return a list containing all the ``<Relationship>`` child elements.

### `add_rel`

```python
add_rel(rId: str, reltype: str, target: str, is_external: bool = False)
```

Add a child ``<Relationship>`` element with attributes set according to
parameter values.

### `new`

```python
new() -> CT_Relationships
```

Return a new ``<Relationships>`` element.

### `xml`

```python
xml
```

Return XML string for this element, suitable for saving in a .rels stream,
not pretty printed and with an XML declaration at the top.

## `CT_Types`

Bases: `BaseOxmlElement`

``<Types>`` element, the container element for Default and Override elements in
[Content_Types].xml.

### `add_default`

```python
add_default(ext, content_type)
```

Add a child ``<Default>`` element with attributes set to parameter values.

### `add_override`

```python
add_override(partname, content_type)
```

Add a child ``<Override>`` element with attributes set to parameter
values.

### `defaults`

```python
defaults
```

### `new`

```python
new()
```

Return a new ``<Types>`` element.

### `overrides`

```python
overrides
```

## `ct_namespace`

```python
ct_namespace = element_class_lookup.get_namespace(nsmap['ct'])
```

## `element_class_lookup`

```python
element_class_lookup = etree.ElementNamespaceClassLookup()
```

## `nsmap`

```python
nsmap = {'ct': NS.OPC_CONTENT_TYPES, 'pr': NS.OPC_RELATIONSHIPS, 'r': NS.OFC_RELATIONSHIPS}
```

## `oxml_parser`

```python
oxml_parser = etree.XMLParser(remove_blank_text=True, resolve_entities=False)
```

## `parse_xml`

```python
parse_xml(text: str) -> etree._Element
```

`etree.fromstring()` replacement that uses oxml parser.

## `pr_namespace`

```python
pr_namespace = element_class_lookup.get_namespace(nsmap['pr'])
```

## `qn`

```python
qn(tag: str) -> str
```

Stands for "qualified name", a utility function to turn a namespace prefixed tag
name into a Clark-notation qualified tag name for lxml.

For
example, ``qn('p:cSld')`` returns ``'{http://schemas.../main}cSld'``.

## `serialize_for_reading`

```python
serialize_for_reading(element: etree._Element) -> str
```

Serialize `element` to human-readable XML suitable for tests.

No XML declaration.

## `serialize_part_xml`

```python
serialize_part_xml(part_elm: etree._Element) -> bytes
```

Serialize `part_elm` etree element to XML suitable for storage as an XML part.

That is to say, no insignificant whitespace added for readability, and an
appropriate XML declaration added with UTF-8 encoding specified.
