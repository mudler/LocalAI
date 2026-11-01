<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.shared`

Objects shared by modules in the docx.oxml subpackage.

## `CT_DecimalNumber`

Bases: `BaseOxmlElement`

Used for ``<w:numId>``, ``<w:ilvl>``, ``<w:abstractNumId>`` and several others,
containing a text representation of a decimal number (e.g. 42) in its ``val``
attribute.

### `new`

```python
new(nsptagname: str, val: int)
```

Return a new ``CT_DecimalNumber`` element having tagname `nsptagname` and
``val`` attribute set to `val`.

### `val`

```python
val: int = RequiredAttribute('w:val', ST_DecimalNumber)
```

## `CT_OnOff`

Bases: `BaseOxmlElement`

Used for `w:b`, `w:i` elements and others.

Contains a bool-ish string in its `val` attribute, xsd:boolean plus "on" and
"off". Defaults to `True`, so `<w:b>` for example means "bold is turned on".

### `val`

```python
val: bool = OptionalAttribute('w:val', ST_OnOff, default=True)
```

## `CT_String`

Bases: `BaseOxmlElement`

Used for `w:pStyle` and `w:tblStyle` elements and others.

In those cases, it containing a style name in its `val` attribute.

### `new`

```python
new(nsptagname: str, val: str)
```

A new `CT_String`` element with tagname `nsptagname` and `val` attribute set to `val`.

### `val`

```python
val: str = RequiredAttribute('w:val', ST_String)
```
