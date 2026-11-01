<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.styles.fills`

## `DEFAULT_EMPTY_FILL`

```python
DEFAULT_EMPTY_FILL = PatternFill()
```

## `DEFAULT_GRAY_FILL`

```python
DEFAULT_GRAY_FILL = PatternFill(patternType='gray125')
```

## `FILL_NONE`

```python
FILL_NONE = 'none'
```

## `FILL_PATTERN_DARKDOWN`

```python
FILL_PATTERN_DARKDOWN = 'darkDown'
```

## `FILL_PATTERN_DARKGRAY`

```python
FILL_PATTERN_DARKGRAY = 'darkGray'
```

## `FILL_PATTERN_DARKGRID`

```python
FILL_PATTERN_DARKGRID = 'darkGrid'
```

## `FILL_PATTERN_DARKHORIZONTAL`

```python
FILL_PATTERN_DARKHORIZONTAL = 'darkHorizontal'
```

## `FILL_PATTERN_DARKTRELLIS`

```python
FILL_PATTERN_DARKTRELLIS = 'darkTrellis'
```

## `FILL_PATTERN_DARKUP`

```python
FILL_PATTERN_DARKUP = 'darkUp'
```

## `FILL_PATTERN_DARKVERTICAL`

```python
FILL_PATTERN_DARKVERTICAL = 'darkVertical'
```

## `FILL_PATTERN_GRAY0625`

```python
FILL_PATTERN_GRAY0625 = 'gray0625'
```

## `FILL_PATTERN_GRAY125`

```python
FILL_PATTERN_GRAY125 = 'gray125'
```

## `FILL_PATTERN_LIGHTDOWN`

```python
FILL_PATTERN_LIGHTDOWN = 'lightDown'
```

## `FILL_PATTERN_LIGHTGRAY`

```python
FILL_PATTERN_LIGHTGRAY = 'lightGray'
```

## `FILL_PATTERN_LIGHTGRID`

```python
FILL_PATTERN_LIGHTGRID = 'lightGrid'
```

## `FILL_PATTERN_LIGHTHORIZONTAL`

```python
FILL_PATTERN_LIGHTHORIZONTAL = 'lightHorizontal'
```

## `FILL_PATTERN_LIGHTTRELLIS`

```python
FILL_PATTERN_LIGHTTRELLIS = 'lightTrellis'
```

## `FILL_PATTERN_LIGHTUP`

```python
FILL_PATTERN_LIGHTUP = 'lightUp'
```

## `FILL_PATTERN_LIGHTVERTICAL`

```python
FILL_PATTERN_LIGHTVERTICAL = 'lightVertical'
```

## `FILL_PATTERN_MEDIUMGRAY`

```python
FILL_PATTERN_MEDIUMGRAY = 'mediumGray'
```

## `FILL_SOLID`

```python
FILL_SOLID = 'solid'
```

## `Fill`

Bases: `Serialisable`

Base class

### `from_tree`

```python
from_tree(el)
```

### `tagname`

```python
tagname = 'fill'
```

## `GradientFill`

```python
GradientFill(type = 'linear', degree = 0, left = 0, right = 0, top = 0, bottom = 0, stop = ())
```

Bases: `Fill`

Fill areas with gradient

Two types of gradient fill are supported:

    - A type='linear' gradient interpolates colours between
      a set of specified Stops, across the length of an area.
      The gradient is left-to-right by default, but this
      orientation can be modified with the degree
      attribute.  A list of Colors can be provided instead
      and they will be positioned with equal distance between them.

    - A type='path' gradient applies a linear gradient from each
      edge of the area. Attributes top, right, bottom, left specify
      the extent of fill from the respective borders. Thus top="0.2"
      will fill the top 20% of the cell.

### `bottom`

```python
bottom = bottom
```

### `degree`

```python
degree = degree
```

### `fill_type`

```python
fill_type = Alias('type')
```

### `left`

```python
left = left
```

### `right`

```python
right = right
```

### `stop`

```python
stop = stop
```

### `tagname`

```python
tagname = 'gradientFill'
```

### `to_tree`

```python
to_tree(tagname = None, namespace = None, idx = None)
```

### `top`

```python
top = top
```

### `type`

```python
type = type
```

## `PatternFill`

```python
PatternFill(patternType = None, fgColor = Color(), bgColor = Color(), fill_type = None, start_color = None, end_color = None)
```

Bases: `Fill`

Area fill patterns for use in styles.
Caution: if you do not specify a fill_type, other attributes will have
no effect !

### `bgColor`

```python
bgColor = bgColor
```

### `end_color`

```python
end_color = Alias('bgColor')
```

### `fgColor`

```python
fgColor = fgColor
```

### `fill_type`

```python
fill_type = Alias('patternType')
```

### `patternType`

```python
patternType = patternType
```

### `start_color`

```python
start_color = Alias('fgColor')
```

### `tagname`

```python
tagname = 'patternFill'
```

### `to_tree`

```python
to_tree(tagname = None, idx = None)
```

## `Stop`

```python
Stop(color, position)
```

Bases: `Serialisable`

### `color`

```python
color = color
```

### `position`

```python
position = position
```

### `tagname`

```python
tagname = 'stop'
```

## `StopList`

Bases: `Sequence`

### `expected_type`

```python
expected_type = Stop
```

## `fills`

```python
fills = (FILL_SOLID, FILL_PATTERN_DARKDOWN, FILL_PATTERN_DARKGRAY, FILL_PATTERN_DARKGRID, FILL_PATTERN_DARKHORIZONTAL, FILL_PATTERN_DARKTRELLIS, FILL_PATTERN_DARKUP, FILL_PATTERN_DARKVERTICAL, FILL_PATTERN_GRAY0625, FILL_PATTERN_GRAY125, FILL_PATTERN_LIGHTDOWN, FILL_PATTERN_LIGHTGRAY, FILL_PATTERN_LIGHTGRID, FILL_PATTERN_LIGHTHORIZONTAL, FILL_PATTERN_LIGHTTRELLIS, FILL_PATTERN_LIGHTUP, FILL_PATTERN_LIGHTVERTICAL, FILL_PATTERN_MEDIUMGRAY)
```
