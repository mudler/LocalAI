<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.simpletypes`

Simple-type classes.

## `BaseFloatType`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

### `validate`

```python
validate(value)
```

## `BaseIntType`

Bases: `BaseSimpleType`

### `convert_from_percent_literal`

```python
convert_from_percent_literal(str_value)
```

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

### `validate`

```python
validate(value)
```

## `BaseSimpleType`

### `from_xml`

```python
from_xml(xml_value: str) -> Any
```

### `to_xml`

```python
to_xml(value: Any) -> str
```

### `validate_float`

```python
validate_float(value: Any)
```

Note that int values are accepted.

### `validate_float_in_range`

```python
validate_float_in_range(value, min_inclusive, max_inclusive)
```

### `validate_int`

```python
validate_int(value)
```

### `validate_int_in_range`

```python
validate_int_in_range(value, min_inclusive, max_inclusive)
```

### `validate_string`

```python
validate_string(value)
```

## `BaseStringEnumerationType`

Bases: `BaseStringType`

### `validate`

```python
validate(value)
```

## `BaseStringType`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

### `validate`

```python
validate(value)
```

## `ST_Angle`

Bases: `XsdInt`

Valid values for `rot` attribute on `<a:xfrm>` element. 60000ths of
a degree rotation.

### `DEGREE_INCREMENTS`

```python
DEGREE_INCREMENTS = 60000
```

### `THREE_SIXTY`

```python
THREE_SIXTY = 360 * DEGREE_INCREMENTS
```

### `convert_from_xml`

```python
convert_from_xml(str_value: str) -> float
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

Convert signed angle float like -42.42 to int 60000 per degree,
normalized to positive value.

### `validate`

```python
validate(value)
```

## `ST_AxisUnit`

Bases: `XsdDouble`

Valid values for val attribute on c:majorUnit and others.

### `validate`

```python
validate(value)
```

## `ST_BarDir`

Bases: `XsdStringEnumeration`

Valid values for <c:barDir val="?"> attribute

### `BAR`

```python
BAR = 'bar'
```

### `COL`

```python
COL = 'col'
```

## `ST_BubbleScale`

Bases: `BaseIntType`

String value is an integer in range 0-300, representing a percent,
optionally including a '%' suffix.

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `validate`

```python
validate(value)
```

## `ST_ContentType`

Bases: `XsdString`

Has a pretty wicked regular expression it needs to match in the schema,
but figuring it's not worth the trouble or run time to identify
a programming error (as opposed to a user/runtime error).

## `ST_Coordinate`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

### `validate`

```python
validate(value)
```

## `ST_Coordinate32`

Bases: `BaseSimpleType`

xsd:union of ST_Coordinate32Unqualified, ST_UniversalMeasure

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

### `validate`

```python
validate(value)
```

## `ST_Coordinate32Unqualified`

Bases: `XsdInt`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

## `ST_CoordinateUnqualified`

Bases: `XsdLong`

### `validate`

```python
validate(value)
```

## `ST_Direction`

Bases: `XsdTokenEnumeration`

Valid values for `<p:ph orient="...">` attribute.

### `HORZ`

```python
HORZ = 'horz'
```

### `VERT`

```python
VERT = 'vert'
```

## `ST_DrawingElementId`

Bases: `XsdUnsignedInt`

## `ST_Extension`

Bases: `XsdString`

Has a regular expression it needs to match in the schema, but figuring
it's not worth the trouble or run time to identify a programming error
(as opposed to a user/runtime error).

## `ST_GapAmount`

Bases: `BaseIntType`

String value is an integer in range 0-500, representing a percent,
optionally including a '%' suffix.

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `validate`

```python
validate(value)
```

## `ST_Grouping`

Bases: `XsdStringEnumeration`

Valid values for <c:grouping val=""> attribute. Overloaded for use as
ST_BarGrouping using same tag name.

### `CLUSTERED`

```python
CLUSTERED = 'clustered'
```

### `PERCENT_STACKED`

```python
PERCENT_STACKED = 'percentStacked'
```

### `STACKED`

```python
STACKED = 'stacked'
```

### `STANDARD`

```python
STANDARD = 'standard'
```

## `ST_HexColorRGB`

Bases: `BaseStringType`

### `convert_to_xml`

```python
convert_to_xml(value)
```

Keep alpha characters all uppercase just for consistency.

### `validate`

```python
validate(value)
```

## `ST_LayoutMode`

Bases: `XsdStringEnumeration`

Valid values for `val` attribute on c:xMode and other elements of type
CT_LayoutMode.

### `EDGE`

```python
EDGE = 'edge'
```

### `FACTOR`

```python
FACTOR = 'factor'
```

## `ST_LblOffset`

Bases: `XsdUnsignedShort`

Unsigned integer value between 0 and 1000 inclusive, with optional
percent character ('%') suffix.

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `validate`

```python
validate(value)
```

## `ST_LineWidth`

Bases: `XsdInt`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `validate`

```python
validate(value)
```

## `ST_MarkerSize`

Bases: `XsdUnsignedByte`

### `validate`

```python
validate(value)
```

## `ST_Orientation`

Bases: `XsdStringEnumeration`

Valid values for `val` attribute on c:orientation (CT_Orientation).

### `MAX_MIN`

```python
MAX_MIN = 'maxMin'
```

### `MIN_MAX`

```python
MIN_MAX = 'minMax'
```

## `ST_Overlap`

Bases: `BaseIntType`

String value is an integer in range -100..100, representing a percent,
optionally including a '%' suffix.

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `validate`

```python
validate(value)
```

## `ST_Percentage`

Bases: `BaseIntType`

Percentage value like 42000 or '42.0%'

Either an integer literal representing 1000ths of a percent
(e.g. "42000"), or a floating point literal with a '%' suffix
(e.g. "42.0%).

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

### `validate`

```python
validate(value)
```

## `ST_PlaceholderSize`

Bases: `XsdTokenEnumeration`

Valid values for <p:ph> sz (size) attribute

### `FULL`

```python
FULL = 'full'
```

### `HALF`

```python
HALF = 'half'
```

### `QUARTER`

```python
QUARTER = 'quarter'
```

## `ST_PositiveCoordinate`

Bases: `XsdLong`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `validate`

```python
validate(value)
```

## `ST_PositiveFixedAngle`

Bases: `ST_Angle`

Valid values for `a:lin@ang`.

60000ths of a degree rotation, constained to positive angles less than
360 degrees.

### `convert_to_xml`

```python
convert_to_xml(degrees)
```

Convert signed angle float like -427.42 to int 60000 per degree.

Value is normalized to a positive value less than 360 degrees.

## `ST_PositiveFixedPercentage`

Bases: `ST_Percentage`

Percentage value between 0 and 100% like 42000 or '42.0%'

Either an integer literal representing 1000ths of a percent
(e.g. "42000"), or a floating point literal with a '%' suffix
(e.g. "42.0%). Value is constrained to range of 0% to 100%. The source
value is a float between 0.0 and 1.0.

### `validate`

```python
validate(value)
```

## `ST_RelationshipId`

Bases: `XsdString`

## `ST_SlideId`

Bases: `XsdUnsignedInt`

### `validate`

```python
validate(value)
```

## `ST_SlideSizeCoordinate`

Bases: `BaseIntType`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `validate`

```python
validate(value)
```

## `ST_Style`

Bases: `XsdUnsignedByte`

### `validate`

```python
validate(value)
```

## `ST_TargetMode`

Bases: `XsdString`

The valid values for the ``TargetMode`` attribute in a Relationship
element, either 'External' or 'Internal'.

### `validate`

```python
validate(value)
```

## `ST_TextAutonumberScheme`

Bases: `XsdTokenEnumeration`

Valid values for `a:buAutoNum/@type` (ECMA-376 ST_TextAutonumberScheme).

## `ST_TextBulletSizePercent`

Bases: `BaseFloatType`

Valid values for `a:buSzPct/@val`, as a fraction of the text size (e.g. 0.75 for 75%).

Reads both wire forms found in real files: thousandths-of-a-percent integers ("75000",
ECMA-376:2006 form) and percent strings ("75%"). Writes the percent-string form, which
both current schema editions bless — their pattern admits WHOLE percents only (25%–400%),
so fractional percents like 0.755 are rejected at validation.

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

Read a bullet size as a fraction, accepting the percent-string and thousandths-of-a-
percent forms.

### `convert_to_xml`

```python
convert_to_xml(value)
```

Write a bullet size fraction as a percent string.

### `validate`

```python
validate(value)
```

Refuse a bullet size outside 0.25..4.0, or one finer than a whole percent.

## `ST_TextBulletStartAtNum`

Bases: `BaseIntType`

Valid values for `a:buAutoNum/@startAt`.

### `validate`

```python
validate(value)
```

Refuse a list start number outside the 1..32767 the schema allows.

## `ST_TextFontScalePercentOrPercentString`

Bases: `BaseFloatType`

Valid values for the `fontScale` attribute of ``<a:normAutofit>``.
Translates to a float value.

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

### `validate`

```python
validate(value)
```

## `ST_TextFontSize`

Bases: `BaseIntType`

### `validate`

```python
validate(value)
```

## `ST_TextIndent`

Bases: `ST_Coordinate32Unqualified`

Valid values for `a:pPr/@indent`, an EMU `Length` (negative = hanging indent).

### `validate`

```python
validate(value)
```

Refuse an indent outside -51206400..51206400 EMU (plus or minus 56 inches), or a non-
integer.

## `ST_TextIndentLevelType`

Bases: `BaseIntType`

### `validate`

```python
validate(value)
```

## `ST_TextLineSpaceReductionPercentOrPercentString`

Bases: `BaseFloatType`

Valid values for `a:normAutofit/@lnSpcReduction`, as a percent float (20.0 = 20%).

Reads both wire forms ("20000" thousandths and "20%"), mirroring the `fontScale`
attribute's simpletype, and writes the thousandths form PowerPoint writes.

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

Read a line-space reduction as a percent, accepting the percent-string and thousandths
forms.

### `convert_to_xml`

```python
convert_to_xml(value)
```

Write a line-space reduction percent in thousandths.

### `validate`

```python
validate(value)
```

Refuse a line-space reduction outside 0.0..100.0 percent.

## `ST_TextMargin`

Bases: `ST_Coordinate32Unqualified`

Valid values for `a:pPr/@marL` and `@marR`, an EMU `Length`.

### `validate`

```python
validate(value)
```

Refuse a margin outside 0..51206400 EMU (0 to 56 inches), or a non-integer.

Negatives are refused here, unlike `indent`.

## `ST_TextSpacingPercentOrPercentString`

Bases: `BaseFloatType`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

1.75 -> '175000'

### `validate`

```python
validate(value)
```

## `ST_TextSpacingPoint`

Bases: `BaseIntType`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

Reads string integer centipoints, returns `Length` value.

### `convert_to_xml`

```python
convert_to_xml(value)
```

### `validate`

```python
validate(value)
```

## `ST_TextTypeface`

Bases: `XsdString`

## `ST_TextWrappingType`

Bases: `XsdTokenEnumeration`

Valid values for <a:bodyPr wrap=""> attribute

### `NONE`

```python
NONE = 'none'
```

### `SQUARE`

```python
SQUARE = 'square'
```

## `ST_UniversalMeasure`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

## `XsdAnyUri`

Bases: `BaseStringType`

There's a regular expression this is supposed to meet but so far thinking
spending cycles on validating wouldn't be worth it for the number of
programming errors it would catch.

## `XsdBoolean`

Bases: `BaseSimpleType`

### `convert_from_xml`

```python
convert_from_xml(str_value)
```

### `convert_to_xml`

```python
convert_to_xml(value)
```

### `validate`

```python
validate(value)
```

## `XsdDouble`

Bases: `BaseFloatType`

## `XsdId`

Bases: `BaseStringType`

String that must begin with a letter or underscore and cannot contain any
colons. Not fully validated because not used in external API.

## `XsdInt`

Bases: `BaseIntType`

### `validate`

```python
validate(value)
```

## `XsdLong`

Bases: `BaseIntType`

### `validate`

```python
validate(value)
```

## `XsdString`

Bases: `BaseStringType`

## `XsdStringEnumeration`

Bases: `BaseStringEnumerationType`

Set of enumerated xsd:string values.

## `XsdToken`

Bases: `BaseStringType`

xsd:string with whitespace collapsing, e.g. multiple spaces reduced to
one, leading and trailing space stripped.

## `XsdTokenEnumeration`

Bases: `BaseStringEnumerationType`

xsd:string with whitespace collapsing, e.g. multiple spaces reduced to
one, leading and trailing space stripped.

## `XsdUnsignedByte`

Bases: `BaseIntType`

### `validate`

```python
validate(value)
```

## `XsdUnsignedInt`

Bases: `BaseIntType`

### `validate`

```python
validate(value)
```

## `XsdUnsignedShort`

Bases: `BaseIntType`

### `validate`

```python
validate(value)
```
