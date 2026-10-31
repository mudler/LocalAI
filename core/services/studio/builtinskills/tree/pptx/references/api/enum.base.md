<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.enum.base`

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
from_xml(xml_value: str) -> Self
```

Enumeration member corresponding to XML attribute value `xml_value`.

Raises `ValueError` if `xml_value` is the empty string ("") or is not an XML attribute
value registered on the enumeration. Note that enum members that do not correspond to one
of the defined values for an XML attribute have `xml_value == ""`. These
"return-value only" members cannot be automatically mapped from an XML attribute value and
must be selected explicitly by code, based on the appropriate conditions.

Example::

    >>> WD_PARAGRAPH_ALIGNMENT.from_xml("center")
    WD_PARAGRAPH_ALIGNMENT.CENTER

### `to_xml`

```python
to_xml(value: int | _T) -> str
```

XML value of this enum member, generally an XML attribute value.

### `validate`

```python
validate(value: _T)
```

Raise `ValueError` if `value` is not an assignable value.

### `xml_value`

```python
xml_value: str | None
```

## `DocsPageFormatter`

```python
DocsPageFormatter(clsname: str, clsdict: dict[str, Any])
```

Bases: `object`

Formats a reStructuredText documention page (string) for an enumeration.

### `page_str`

```python
page_str
```

The RestructuredText documentation page for the enumeration. This is
the only API member for the class.
