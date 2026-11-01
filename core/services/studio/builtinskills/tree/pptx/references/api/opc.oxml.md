<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.opc.oxml`

OPC-local oxml module to handle OPC-local concerns like relationship parsing.

## `CT_Default`

Bases: `BaseOxmlElement`

`<Default>` element.

Specifies the default content type to be applied to a part with the specified extension.

### `contentType`

```python
contentType: str = RequiredAttribute('ContentType', ST_ContentType)
```

### `extension`

```python
extension: str = RequiredAttribute('Extension', ST_Extension)
```

## `CT_Override`

Bases: `BaseOxmlElement`

`<Override>` element.

Specifies the content type to be applied for a part with the specified partname.

### `contentType`

```python
contentType: str = RequiredAttribute('ContentType', ST_ContentType)
```

### `partName`

```python
partName: str = RequiredAttribute('PartName', XsdAnyUri)
```

## `CT_Relationship`

Bases: `BaseOxmlElement`

`<Relationship>` element.

Represents a single relationship from a source to a target part.

### `new`

```python
new(rId: str, reltype: str, target_ref: str, target_mode: str = RTM.INTERNAL) -> CT_Relationship
```

Return a new `<Relationship>` element.

`target_ref` is either a partname or a URI.

### `rId`

```python
rId: str = RequiredAttribute('Id', XsdId)
```

### `reltype`

```python
reltype: str = RequiredAttribute('Type', XsdAnyUri)
```

### `targetMode`

```python
targetMode: str = OptionalAttribute('TargetMode', ST_TargetMode, default=RTM.INTERNAL)
```

### `target_ref`

```python
target_ref: str = RequiredAttribute('Target', XsdAnyUri)
```

## `CT_Relationships`

Bases: `BaseOxmlElement`

`<Relationships>` element, the root element in a .rels file.

### `add_rel`

```python
add_rel(rId: str, reltype: str, target: str, is_external: bool = False) -> CT_Relationship
```

Add a child `<Relationship>` element with attributes set as specified.

### `new`

```python
new() -> CT_Relationships
```

Return a new `<Relationships>` element.

### `relationship`

```python
relationship = ZeroOrMore('pr:Relationship')
```

### `relationship_lst`

```python
relationship_lst: list[CT_Relationship]
```

### `xml_file_bytes`

```python
xml_file_bytes: bytes
```

Return XML bytes, with XML-declaration, for this `<Relationships>` element.

Suitable for saving in a .rels stream, not pretty printed and with an XML declaration at
the top.

## `CT_Types`

Bases: `BaseOxmlElement`

`<Types>` element.

The container element for Default and Override elements in [Content_Types].xml.

### `add_default`

```python
add_default(ext: str, content_type: str) -> CT_Default
```

Add a child `<Default>` element with attributes set to parameter values.

### `add_override`

```python
add_override(partname: PackURI, content_type: str) -> CT_Override
```

Add a child `<Override>` element with attributes set to parameter values.

### `default`

```python
default = ZeroOrMore('ct:Default')
```

### `default_lst`

```python
default_lst: list[CT_Default]
```

### `new`

```python
new() -> CT_Types
```

Return a new `<Types>` element.

### `override`

```python
override = ZeroOrMore('ct:Override')
```

### `override_lst`

```python
override_lst: list[CT_Override]
```

## `nsmap`

```python
nsmap = {'ct': NS.OPC_CONTENT_TYPES, 'pr': NS.OPC_RELATIONSHIPS, 'r': NS.OFC_RELATIONSHIPS}
```

## `oxml_to_encoded_bytes`

```python
oxml_to_encoded_bytes(element: BaseOxmlElement, encoding: str = 'utf-8', pretty_print: bool = False, standalone: bool | None = None) -> bytes
```

## `oxml_tostring`

```python
oxml_tostring(elm: BaseOxmlElement, encoding: str | None = None, pretty_print: bool = False, standalone: bool | None = None)
```

## `serialize_part_xml`

```python
serialize_part_xml(part_elm: BaseOxmlElement) -> bytes
```

Produce XML-file bytes for `part_elm`, suitable for writing directly to a `.xml` file.

Includes XML-declaration header.
