<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.oxml.dml.fill`

lxml custom element classes for DrawingML-related XML elements.

## `CT_Blip`

Bases: `BaseOxmlElement`

<a:blip> element

### `rEmbed`

```python
rEmbed = OptionalAttribute('r:embed', ST_RelationshipId)
```

## `CT_BlipFillProperties`

Bases: `BaseOxmlElement`

Custom element class for <a:blipFill> element.

### `blip`

```python
blip = ZeroOrOne('a:blip', successors=_tag_seq[1:])
```

### `crop`

```python
crop(cropping)
```

Set `a:srcRect` child to crop according to *cropping* values.

### `srcRect`

```python
srcRect = ZeroOrOne('a:srcRect', successors=_tag_seq[2:])
```

## `CT_GradientFillProperties`

Bases: `BaseOxmlElement`

`a:gradFill` custom element class.

### `gsLst`

```python
gsLst = ZeroOrOne('a:gsLst', successors=_tag_seq[1:])
```

### `lin`

```python
lin = ZeroOrOne('a:lin', successors=_tag_seq[2:])
```

### `new_gradFill`

```python
new_gradFill()
```

Return newly-created "loose" default gradient subtree.

### `path`

```python
path = ZeroOrOne('a:path', successors=_tag_seq[3:])
```

## `CT_GradientStop`

Bases: `BaseOxmlElement`

`a:gs` custom element class.

### `eg_colorChoice`

```python
eg_colorChoice = ZeroOrOneChoice((Choice('a:scrgbClr'), Choice('a:srgbClr'), Choice('a:hslClr'), Choice('a:sysClr'), Choice('a:schemeClr'), Choice('a:prstClr')), successors=())
```

### `pos`

```python
pos = RequiredAttribute('pos', ST_PositiveFixedPercentage)
```

## `CT_GradientStopList`

Bases: `BaseOxmlElement`

`a:gsLst` custom element class.

### `gs`

```python
gs = OneOrMore('a:gs')
```

### `new_gsLst`

```python
new_gsLst()
```

Return newly-created "loose" default stop-list subtree.

An `a:gsLst` element must have at least two `a:gs` children. These
are the default from the PowerPoint built-in "White" template.

## `CT_GroupFillProperties`

Bases: `BaseOxmlElement`

`a:grpFill` custom element class

## `CT_LinearShadeProperties`

Bases: `BaseOxmlElement`

`a:lin` custom element class

### `ang`

```python
ang = OptionalAttribute('ang', ST_PositiveFixedAngle)
```

## `CT_NoFillProperties`

Bases: `BaseOxmlElement`

`a:noFill` custom element class

## `CT_PatternFillProperties`

Bases: `BaseOxmlElement`

`a:pattFill` custom element class

### `bgClr`

```python
bgClr = ZeroOrOne('a:bgClr', successors=_tag_seq[2:])
```

### `fgClr`

```python
fgClr = ZeroOrOne('a:fgClr', successors=_tag_seq[1:])
```

### `prst`

```python
prst = OptionalAttribute('prst', MSO_PATTERN_TYPE)
```

## `CT_RelativeRect`

Bases: `BaseOxmlElement`

`a:srcRect` element and perhaps others.

### `b`

```python
b = OptionalAttribute('b', ST_Percentage, default=0.0)
```

### `l`

```python
l = OptionalAttribute('l', ST_Percentage, default=0.0)
```

### `r`

```python
r = OptionalAttribute('r', ST_Percentage, default=0.0)
```

### `t`

```python
t = OptionalAttribute('t', ST_Percentage, default=0.0)
```

## `CT_SolidColorFillProperties`

Bases: `BaseOxmlElement`

`a:solidFill` custom element class.

### `eg_colorChoice`

```python
eg_colorChoice = ZeroOrOneChoice((Choice('a:scrgbClr'), Choice('a:srgbClr'), Choice('a:hslClr'), Choice('a:sysClr'), Choice('a:schemeClr'), Choice('a:prstClr')), successors=())
```
