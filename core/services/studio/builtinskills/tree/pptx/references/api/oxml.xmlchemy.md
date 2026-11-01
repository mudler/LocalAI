<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.xmlchemy`

Base and meta classes enabling declarative definition of custom element classes.

## `AttributeType`

Bases: `Protocol`

Interface for an object that can act as an attribute type.

An attribute-type specifies how values are transformed to and from the XML "string" value of the
attribute.

### `from_xml`

```python
from_xml(xml_value: str) -> Any
```

Transform an attribute value to a Python value.

### `to_xml`

```python
to_xml(value: Any) -> str
```

Transform a Python value to a str value suitable to this XML attribute.

## `BaseAttribute`

```python
BaseAttribute(attr_name: str, simple_type: type[AttributeType])
```

Base class for OptionalAttribute and RequiredAttribute, providing common methods.

### `populate_class_members`

```python
populate_class_members(element_cls: Type[BaseOxmlElement], prop_name: str)
```

Add the appropriate methods to *element_cls*.

## `BaseOxmlElement`

Bases: `ElementBase`

Effective base class for all custom element classes.

Adds standardized behavior to all classes in one place.

### `first_child_found_in`

```python
first_child_found_in(*tagnames: str) -> _Element | None
```

First child with tag in `tagnames`, or None if not found.

### `insert_element_before`

```python
insert_element_before(elm: ElementBase, *tagnames: str)
```

### `remove_all`

```python
remove_all(*tagnames: str) -> None
```

Remove child elements with tagname (e.g. "a:p") in `tagnames`.

### `xml`

```python
xml: str
```

XML string for this element, suitable for testing purposes.

Pretty printed for readability and without an XML declaration at the top.

### `xpath`

```python
xpath(xpath_str: str) -> Any
```

Override of `lxml` _Element.xpath() method.

Provides standard Open XML namespace mapping (`nsmap`) in centralized location.

## `Choice`

Bases: `_BaseChildElement`

Defines a child element belonging to a group, only one of which may appear as a child.

### `nsptagname`

```python
nsptagname
```

### `populate_class_members`

```python
populate_class_members(element_cls: Type[BaseOxmlElement], group_prop_name: str, successors: Sequence[str])
```

Add the appropriate methods to `element_cls`.

## `MetaOxmlElement`

```python
MetaOxmlElement(clsname: str, bases: tuple[type, ...], clsdict: dict[str, Any])
```

Bases: `type`

Metaclass for BaseOxmlElement.

## `OneAndOnlyOne`

```python
OneAndOnlyOne(nsptagname: str)
```

Bases: `_BaseChildElement`

Defines a required child element for MetaOxmlElement.

### `populate_class_members`

```python
populate_class_members(element_cls: Type[BaseOxmlElement], prop_name: str)
```

Add the appropriate methods to *element_cls*.

## `OneOrMore`

Bases: `_BaseChildElement`

Defines a repeating child element for MetaOxmlElement that must appear at least once.

### `populate_class_members`

```python
populate_class_members(element_cls: Type[BaseOxmlElement], prop_name: str)
```

Add the appropriate methods to *element_cls*.

## `OptionalAttribute`

```python
OptionalAttribute(attr_name: str, simple_type: type[AttributeType], default: Any = None)
```

Bases: `BaseAttribute`

Defines an optional attribute on a custom element class.

An optional attribute returns a default value when not present for reading. When assigned
`None`, the attribute is removed.

## `OxmlElement`

```python
OxmlElement(nsptag_str: str, nsmap: dict[str, str] | None = None) -> BaseOxmlElement
```

Return a "loose" lxml element having the tag specified by `nsptag_str`.

`nsptag_str` must contain the standard namespace prefix, e.g. 'a:tbl'. The resulting element is
an instance of the custom element class for this tag name if one is defined.

## `RequiredAttribute`

Bases: `BaseAttribute`

Defines a required attribute on a custom element class.

A required attribute is assumed to be present for reading, so does not have a default value;
its actual value is always used. If missing on read, an `InvalidXmlError` is raised. It also
does not remove the attribute if `None` is assigned. Assigning `None` raises `TypeError` or
`ValueError`, depending on the simple type of the attribute.

## `XmlString`

Bases: `str`

Provides string comparison override suitable for serialized XML; useful for tests.

## `ZeroOrMore`

Bases: `_BaseChildElement`

Defines an optional repeating child element for MetaOxmlElement.

### `populate_class_members`

```python
populate_class_members(element_cls: Type[BaseOxmlElement], prop_name: str)
```

Add the appropriate methods to *element_cls*.

## `ZeroOrOne`

Bases: `_BaseChildElement`

Defines an optional child element for MetaOxmlElement.

### `populate_class_members`

```python
populate_class_members(element_cls: Type[BaseOxmlElement], prop_name: str)
```

Add the appropriate methods to `element_cls`.

## `ZeroOrOneChoice`

```python
ZeroOrOneChoice(choices: Iterable[Choice], successors: Iterable[str] = ())
```

Bases: `_BaseChildElement`

An `EG_*` element group where at most one of its members may appear as a child.

### `populate_class_members`

```python
populate_class_members(element_cls: Type[BaseOxmlElement], prop_name: str)
```

Add the appropriate methods to `element_cls`.

## `serialize_for_reading`

```python
serialize_for_reading(element: ElementBase)
```

Serialize *element* to human-readable XML suitable for tests. No XML
declaration.
