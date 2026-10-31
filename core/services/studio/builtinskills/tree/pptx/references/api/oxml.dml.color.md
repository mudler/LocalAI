<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.dml.color`

lxml custom element classes for DrawingML-related XML elements.

## `CT_Color`

Bases: `BaseOxmlElement`

Custom element class for `a:fgClr`, `a:bgClr` and perhaps others.

### `eg_colorChoice`

```python
eg_colorChoice = ZeroOrOneChoice((Choice('a:scrgbClr'), Choice('a:srgbClr'), Choice('a:hslClr'), Choice('a:sysClr'), Choice('a:schemeClr'), Choice('a:prstClr')), successors=())
```

## `CT_HslColor`

Bases: `_BaseColorElement`

Custom element class for <a:hslClr> element.

## `CT_Percentage`

Bases: `BaseOxmlElement`

Custom element class for <a:lumMod> and <a:lumOff> elements.

### `val`

```python
val = RequiredAttribute('val', ST_Percentage)
```

## `CT_PresetColor`

Bases: `_BaseColorElement`

Custom element class for <a:prstClr> element.

## `CT_SRgbColor`

Bases: `_BaseColorElement`

Custom element class for <a:srgbClr> element.

### `val`

```python
val = RequiredAttribute('val', ST_HexColorRGB)
```

## `CT_ScRgbColor`

Bases: `_BaseColorElement`

Custom element class for <a:scrgbClr> element.

## `CT_SchemeColor`

Bases: `_BaseColorElement`

Custom element class for <a:schemeClr> element.

### `val`

```python
val = RequiredAttribute('val', MSO_THEME_COLOR)
```

## `CT_SystemColor`

Bases: `_BaseColorElement`

Custom element class for <a:sysClr> element.
