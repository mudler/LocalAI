<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.xmlchemy`

Enabling declarative definition of lxml custom element classes.

## `BaseAttribute`

```python
BaseAttribute(attr_name: str, simple_type: Type[BaseXmlEnum] | Type[BaseSimpleType])
```

Base class for OptionalAttribute and RequiredAttribute.

Provides common methods.

### `populate_class_members`

```python
populate_class_members(element_cls: MetaOxmlElement, prop_name: str) -> None
```

Add the appropriate methods to `element_cls`.

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
populate_class_members(element_cls: MetaOxmlElement, group_prop_name: str, successors: tuple[str, ...]) -> None
```

Add the appropriate methods to `element_cls`.

## `MetaOxmlElement`

```python
MetaOxmlElement(clsname: str, bases: tuple[type, ...], namespace: dict[str, Any])
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
populate_class_members(element_cls: MetaOxmlElement, prop_name: str) -> None
```

Add the appropriate methods to `element_cls`.

## `OneOrMore`

Bases: `_BaseChildElement`

Defines a repeating child element for MetaOxmlElement that must appear at least
once.

### `populate_class_members`

```python
populate_class_members(element_cls: MetaOxmlElement, prop_name: str) -> None
```

Add the appropriate methods to `element_cls`.

## `OptionalAttribute`

```python
OptionalAttribute(attr_name: str, simple_type: Type[BaseXmlEnum] | Type[BaseSimpleType], default: BaseXmlEnum | BaseSimpleType | str | bool | None = None)
```

Bases: `BaseAttribute`

Defines an optional attribute on a custom element class.

An optional attribute returns a default value when not present for reading. When
assigned `None`, the attribute is removed, but still returns the default value when
one is specified.

## `RequiredAttribute`

Bases: `BaseAttribute`

Defines a required attribute on a custom element class.

A required attribute is assumed to be present for reading, so does not have a
default value; its actual value is always used. If missing on read, an
`InvalidXmlError` is raised. It also does not remove the attribute if `None` is
assigned. Assigning `None` raises `TypeError` or `ValueError`, depending on the
simple type of the attribute.

## `XmlString`

Bases: `str`

Provides string comparison override suitable for serialized XML that is useful
for tests.

## `ZeroOrMore`

Bases: `_BaseChildElement`

Defines an optional repeating child element for MetaOxmlElement.

### `populate_class_members`

```python
populate_class_members(element_cls: MetaOxmlElement, prop_name: str) -> None
```

Add the appropriate methods to `element_cls`.

## `ZeroOrOne`

Bases: `_BaseChildElement`

Defines an optional child element for MetaOxmlElement.

### `populate_class_members`

```python
populate_class_members(element_cls: MetaOxmlElement, prop_name: str) -> None
```

Add the appropriate methods to `element_cls`.

## `ZeroOrOneChoice`

```python
ZeroOrOneChoice(choices: Sequence[Choice], successors: tuple[str, ...] = ())
```

Bases: `_BaseChildElement`

Correspondes to an ``EG_*`` element group where at most one of its members may
appear as a child.

### `populate_class_members`

```python
populate_class_members(element_cls: MetaOxmlElement, prop_name: str) -> None
```

Add the appropriate methods to `element_cls`.

## `serialize_for_reading`

```python
serialize_for_reading(element: ElementBase)
```

Serialize `element` to human-readable XML suitable for tests.

No XML declaration.
