<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.cell.text`

Richtext definition

## `InlineFont`

```python
InlineFont(rFont = None, charset = None, family = None, b = None, i = None, strike = None, outline = None, shadow = None, condense = None, extend = None, color = None, sz = None, u = None, vertAlign = None, scheme = None)
```

Bases: `Font`

Font for inline text because, yes what you need are different objects with the same elements but different constraints.

### `b`

```python
b = b
```

### `charset`

```python
charset = charset
```

### `color`

```python
color = color
```

### `condense`

```python
condense = condense
```

### `extend`

```python
extend = extend
```

### `family`

```python
family = family
```

### `i`

```python
i = i
```

### `outline`

```python
outline = outline
```

### `rFont`

```python
rFont = rFont
```

### `scheme`

```python
scheme = scheme
```

### `shadow`

```python
shadow = shadow
```

### `strike`

```python
strike = strike
```

### `sz`

```python
sz = sz
```

### `tagname`

```python
tagname = 'RPrElt'
```

### `u`

```python
u = u
```

### `vertAlign`

```python
vertAlign = vertAlign
```

## `PhoneticProperties`

```python
PhoneticProperties(fontId = None, type = None, alignment = None)
```

Bases: `Serialisable`

### `alignment`

```python
alignment = alignment
```

### `fontId`

```python
fontId = fontId
```

### `tagname`

```python
tagname = 'phoneticPr'
```

### `type`

```python
type = type
```

## `PhoneticText`

```python
PhoneticText(sb = None, eb = None, t = None)
```

Bases: `Serialisable`

### `eb`

```python
eb = eb
```

### `sb`

```python
sb = sb
```

### `t`

```python
t = t
```

### `tagname`

```python
tagname = 'rPh'
```

### `text`

```python
text = Alias('t')
```

## `RichText`

```python
RichText(rPr = None, t = None)
```

Bases: `Serialisable`

### `font`

```python
font = Alias('rPr')
```

### `rPr`

```python
rPr = rPr
```

### `t`

```python
t = t
```

### `tagname`

```python
tagname = 'RElt'
```

### `text`

```python
text = Alias('t')
```

## `Text`

```python
Text(t = None, r = (), rPh = (), phoneticPr = None)
```

Bases: `Serialisable`

### `PhoneticProperties`

```python
PhoneticProperties = Alias('phoneticPr')
```

### `content`

```python
content
```

Text stripped of all formatting

### `formatted`

```python
formatted = Alias('r')
```

### `phonetic`

```python
phonetic = Alias('rPh')
```

### `phoneticPr`

```python
phoneticPr = phoneticPr
```

### `plain`

```python
plain = Alias('t')
```

### `r`

```python
r = r
```

### `rPh`

```python
rPh = rPh
```

### `t`

```python
t = t
```

### `tagname`

```python
tagname = 'text'
```
