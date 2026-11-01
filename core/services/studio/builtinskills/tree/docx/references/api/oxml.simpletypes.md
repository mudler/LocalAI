<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.simpletypes`

Simple-type classes, corresponding to ST_* schema items.

## `BaseIntType`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> int
```

### `convert_to_xml`

```python
convert_to_xml(value: int) -> str
```

### `validate`

```python
validate(value: Any) -> None
```

## `BaseSimpleType`

Base class for simple-types.

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> Any
```

### `convert_to_xml`

```python
convert_to_xml(value: Any) -> str
```

### `from_xml`

```python
from_xml(xml_value: str) -> Any
```

### `to_xml`

```python
to_xml(value: Any) -> str
```

### `validate`

```python
validate(value: Any) -> None
```

### `validate_int`

```python
validate_int(value: object)
```

### `validate_int_in_range`

```python
validate_int_in_range(value: int, min_inclusive: int, max_inclusive: int) -> None
```

### `validate_string`

```python
validate_string(value: Any) -> str
```

## `BaseStringEnumerationType`

Bases: `BaseStringType`

### `validate`

```python
validate(value: Any) -> None
```

## `BaseStringType`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> str
```

### `convert_to_xml`

```python
convert_to_xml(value: str) -> str
```

### `validate`

```python
validate(value: str)
```

## `ST_BrClear`

Bases: `XsdString`

### `validate`

```python
validate(value: str) -> None
```

## `ST_BrType`

Bases: `XsdString`

### `validate`

```python
validate(value: Any) -> None
```

## `ST_Coordinate`

Bases: `BaseIntType`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> Length
```

### `validate`

```python
validate(value: Any) -> None
```

## `ST_CoordinateUnqualified`

Bases: `XsdLong`

### `validate`

```python
validate(value: Any) -> None
```

## `ST_DateTime`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> dt.datetime
```

Convert an xsd:dateTime string to a datetime object.

### `convert_to_xml`

```python
convert_to_xml(value: dt.datetime) -> str
```

### `validate`

```python
validate(value: Any) -> None
```

## `ST_DecimalNumber`

Bases: `XsdInt`

## `ST_DrawingElementId`

Bases: `XsdUnsignedInt`

## `ST_HexColor`

Bases: `BaseStringType`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> RGBColor | str
```

### `convert_to_xml`

```python
convert_to_xml(value: RGBColor) -> str
```

Keep alpha hex numerals all uppercase just for consistency.

### `validate`

```python
validate(value: Any) -> None
```

## `ST_HexColorAuto`

Bases: `XsdStringEnumeration`

Value for `w:color/[@val="auto"] attribute setting.

### `AUTO`

```python
AUTO = 'auto'
```

## `ST_HpsMeasure`

Bases: `XsdUnsignedLong`

Half-point measure, e.g. 24.0 represents 12.0 points.

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> Length
```

### `convert_to_xml`

```python
convert_to_xml(value: int | Length) -> str
```

## `ST_Merge`

Bases: `XsdStringEnumeration`

Valid values for <w:xMerge val=""> attribute.

### `CONTINUE`

```python
CONTINUE = 'continue'
```

### `RESTART`

```python
RESTART = 'restart'
```

## `ST_OnOff`

Bases: `XsdBoolean`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> bool
```

## `ST_PositiveCoordinate`

Bases: `XsdLong`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> Length
```

### `validate`

```python
validate(value: Any) -> None
```

## `ST_RelationshipId`

Bases: `XsdString`

## `ST_SignedTwipsMeasure`

Bases: `XsdInt`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> Length
```

### `convert_to_xml`

```python
convert_to_xml(value: int | Length) -> str
```

## `ST_String`

Bases: `XsdString`

## `ST_TblLayoutType`

Bases: `XsdString`

### `validate`

```python
validate(value: Any) -> None
```

## `ST_TblWidth`

Bases: `XsdString`

### `validate`

```python
validate(value: Any) -> None
```

## `ST_TwipsMeasure`

Bases: `XsdUnsignedLong`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> Length
```

### `convert_to_xml`

```python
convert_to_xml(value: int | Length) -> str
```

## `ST_UniversalMeasure`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> Emu
```

## `ST_VerticalAlignRun`

Bases: `XsdStringEnumeration`

Valid values for `w:vertAlign/@val`.

### `BASELINE`

```python
BASELINE = 'baseline'
```

### `SUBSCRIPT`

```python
SUBSCRIPT = 'subscript'
```

### `SUPERSCRIPT`

```python
SUPERSCRIPT = 'superscript'
```

## `XsdAnyUri`

Bases: `BaseStringType`

There's a regex in the spec this is supposed to meet...

but current assessment is that spending cycles on validating wouldn't be worth it
for the number of programming errors it would catch.

## `XsdBoolean`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> bool
```

### `convert_to_xml`

```python
convert_to_xml(value: bool) -> str
```

### `validate`

```python
validate(value: Any) -> None
```

## `XsdId`

Bases: `BaseStringType`

String that must begin with a letter or underscore and cannot contain any colons.

Not fully validated because not used in external API.

## `XsdInt`

Bases: `BaseIntType`

### `validate`

```python
validate(value: Any) -> None
```

## `XsdLong`

Bases: `BaseIntType`

### `validate`

```python
validate(value: Any) -> None
```

## `XsdString`

Bases: `BaseStringType`

## `XsdStringEnumeration`

Bases: `BaseStringEnumerationType`

Set of enumerated xsd:string values.

## `XsdToken`

Bases: `BaseStringType`

Xsd:string with whitespace collapsing, e.g. multiple spaces reduced to one,
leading and trailing space stripped.

## `XsdUnsignedInt`

Bases: `BaseIntType`

### `validate`

```python
validate(value: Any) -> None
```

## `XsdUnsignedLong`

Bases: `BaseIntType`

### `validate`

```python
validate(value: Any) -> None
```
