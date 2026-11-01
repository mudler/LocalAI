<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.enum.base`

Base classes and other objects used by enumerations.

## `BaseEnum`

Bases: `int`, `Enum`

Base class for Enums that do not map XML attr values.

The enum's value will be an integer, corresponding to the integer assigned the
corresponding member in the MS API enum of the same name.

## `BaseXmlEnum`

Bases: `int`, `Enum`

Base class for Enums that also map XML attr values.

The enum's value will be an integer, corresponding to the integer assigned the
corresponding member in the MS API enum of the same name.

### `from_xml`

```python
from_xml(xml_value: str | None) -> Self
```

Enumeration member corresponding to XML attribute value `xml_value`.

Example::

    >>> WD_PARAGRAPH_ALIGNMENT.from_xml("center")
    WD_PARAGRAPH_ALIGNMENT.CENTER

### `to_xml`

```python
to_xml(value: int | _T | None) -> str | None
```

XML value of this enum member, generally an XML attribute value.

### `xml_value`

```python
xml_value: str | None
```

## `DocsPageFormatter`

```python
DocsPageFormatter(clsname: str, clsdict: Dict[str, Any])
```

Generate an .rst doc page for an enumeration.

Formats a RestructuredText documention page (string) for the enumeration class parts
passed to the constructor. An immutable one-shot service object.

### `page_str`

```python
page_str
```

The RestructuredText documentation page for the enumeration.

This is the only API member for the class.
