<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.chart.shared`

Shared oxml objects for charts.

## `CT_Boolean`

Bases: `BaseOxmlElement`

Common complex type used for elements having a True/False value.

### `val`

```python
val = OptionalAttribute('val', XsdBoolean, default=True)
```

## `CT_Boolean_Explicit`

Bases: `BaseOxmlElement`

Always spells out the `val` attribute, e.g. `val=1`.

At least one boolean element is improperly interpreted by one or more
versions of PowerPoint. The `c:overlay` element is interpreted as `False`
when no `val` attribute is present, contrary to the behavior described in
the schema. A remedy for this is to interpret a missing `val` attribute
as `True` (consistent with the spec), but always write the attribute
whenever there is occasion for changing the element.

### `val`

```python
val
```

## `CT_Double`

Bases: `BaseOxmlElement`

Used for floating point values.

### `val`

```python
val = RequiredAttribute('val', XsdDouble)
```

## `CT_Layout`

Bases: `BaseOxmlElement`

``<c:layout>`` custom element class

### `horz_offset`

```python
horz_offset
```

The float value in ./c:manualLayout/c:x when
c:layout/c:manualLayout/c:xMode@val == "factor". 0.0 if that XPath
expression finds no match.

### `manualLayout`

```python
manualLayout = ZeroOrOne('c:manualLayout', successors=('c:extLst',))
```

## `CT_LayoutMode`

Bases: `BaseOxmlElement`

Used for ``<c:xMode>``, ``<c:yMode>``, ``<c:wMode>``, and ``<c:hMode>``
child elements of CT_ManualLayout.

### `val`

```python
val = OptionalAttribute('val', ST_LayoutMode, default=ST_LayoutMode.FACTOR)
```

## `CT_ManualLayout`

Bases: `BaseOxmlElement`

``<c:manualLayout>`` custom element class

### `horz_offset`

```python
horz_offset
```

The float value in ./c:x@val when ./c:xMode@val == "factor". 0.0 when
./c:x is not present or ./c:xMode@val != "factor".

### `x`

```python
x = ZeroOrOne('c:x', successors=_tag_seq[6:])
```

### `xMode`

```python
xMode = ZeroOrOne('c:xMode', successors=_tag_seq[2:])
```

## `CT_NumFmt`

Bases: `BaseOxmlElement`

``<c:numFmt>`` element specifying the formatting for number labels on a
tick mark or data point.

### `formatCode`

```python
formatCode = RequiredAttribute('formatCode', XsdString)
```

### `sourceLinked`

```python
sourceLinked = OptionalAttribute('sourceLinked', XsdBoolean)
```

## `CT_Title`

Bases: `BaseOxmlElement`

`c:title` custom element class.

### `get_or_add_tx_rich`

```python
get_or_add_tx_rich()
```

Return `c:tx/c:rich`, newly created if not present.

Return the `c:rich` grandchild at `c:tx/c:rich`. Both the `c:tx` and
`c:rich` elements are created if not already present. Any
`c:tx/c:strRef` element is removed. (Such an element would contain
a cell reference for the axis title text in the chart's Excel
worksheet.)

### `new_title`

```python
new_title()
```

Return "loose" `c:title` element containing default children.

### `spPr`

```python
spPr = ZeroOrOne('c:spPr', successors=_tag_seq[4:])
```

### `tx`

```python
tx = ZeroOrOne('c:tx', successors=_tag_seq[1:])
```

### `tx_rich`

```python
tx_rich
```

Return `c:tx/c:rich` or `None` if not present.

## `CT_Tx`

Bases: `BaseOxmlElement`

``<c:tx>`` element containing the text for a label on a data point or
other chart item.

### `rich`

```python
rich = ZeroOrOne('c:rich')
```

### `strRef`

```python
strRef = ZeroOrOne('c:strRef')
```

## `CT_UnsignedInt`

Bases: `BaseOxmlElement`

``<c:idx>`` element and others.

### `val`

```python
val = RequiredAttribute('val', XsdUnsignedInt)
```
