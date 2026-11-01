<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.styles.colors`

## `BLACK`

```python
BLACK = COLOR_INDEX[0]
```

## `BLUE`

```python
BLUE = COLOR_INDEX[4]
```

## `COLOR_INDEX`

```python
COLOR_INDEX = ('00000000', '00FFFFFF', '00FF0000', '0000FF00', '000000FF', '00FFFF00', '00FF00FF', '0000FFFF', '00000000', '00FFFFFF', '00FF0000', '0000FF00', '000000FF', '00FFFF00', '00FF00FF', '0000FFFF', '00800000', '00008000', '00000080', '00808000', '00800080', '00008080', '00C0C0C0', '00808080', '009999FF', '00993366', '00FFFFCC', '00CCFFFF', '00660066', '00FF8080', '000066CC', '00CCCCFF', '00000080', '00FF00FF', '00FFFF00', '0000FFFF', '00800080', '00800000', '00008080', '000000FF', '0000CCFF', '00CCFFFF', '00CCFFCC', '00FFFF99', '0099CCFF', '00FF99CC', '00CC99FF', '00FFCC99', '003366FF', '0033CCCC', '0099CC00', '00FFCC00', '00FF9900', '00FF6600', '00666699', '00969696', '00003366', '00339966', '00003300', '00333300', '00993300', '00993366', '00333399', '00333333')
```

## `Color`

```python
Color(rgb = BLACK, indexed = None, auto = None, theme = None, tint = 0.0, index = None, type = 'rgb')
```

Bases: `Serialisable`

Named colors for use in styles.

### `auto`

```python
auto = Bool()
```

### `index`

```python
index
```

### `indexed`

```python
indexed = Integer()
```

### `rgb`

```python
rgb = RGB()
```

### `tagname`

```python
tagname = 'color'
```

### `theme`

```python
theme = Integer()
```

### `tint`

```python
tint = tint
```

### `type`

```python
type = String()
```

### `value`

```python
value
```

## `ColorDescriptor`

Bases: `Typed`

### `expected_type`

```python
expected_type = Color
```

## `ColorList`

```python
ColorList(indexedColors = (), mruColors = ())
```

Bases: `Serialisable`

### `index`

```python
index
```

### `indexedColors`

```python
indexedColors = indexedColors
```

### `mruColors`

```python
mruColors = mruColors
```

### `tagname`

```python
tagname = 'colors'
```

## `RGB`

Bases: `Typed`

Descriptor for aRGB values
If not supplied alpha is 00

### `expected_type`

```python
expected_type = str
```

## `RgbColor`

```python
RgbColor(rgb = None)
```

Bases: `Serialisable`

### `rgb`

```python
rgb = rgb
```

### `tagname`

```python
tagname = 'rgbColor'
```

## `WHITE`

```python
WHITE = COLOR_INDEX[1]
```

## `aRGB_REGEX`

```python
aRGB_REGEX = re.compile('^([A-Fa-f0-9]{8}|[A-Fa-f0-9]{6})$')
```
