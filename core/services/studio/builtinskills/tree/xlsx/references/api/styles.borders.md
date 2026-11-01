<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.styles.borders`

## `BORDER_DASHDOT`

```python
BORDER_DASHDOT = 'dashDot'
```

## `BORDER_DASHDOTDOT`

```python
BORDER_DASHDOTDOT = 'dashDotDot'
```

## `BORDER_DASHED`

```python
BORDER_DASHED = 'dashed'
```

## `BORDER_DOTTED`

```python
BORDER_DOTTED = 'dotted'
```

## `BORDER_DOUBLE`

```python
BORDER_DOUBLE = 'double'
```

## `BORDER_HAIR`

```python
BORDER_HAIR = 'hair'
```

## `BORDER_MEDIUM`

```python
BORDER_MEDIUM = 'medium'
```

## `BORDER_MEDIUMDASHDOT`

```python
BORDER_MEDIUMDASHDOT = 'mediumDashDot'
```

## `BORDER_MEDIUMDASHDOTDOT`

```python
BORDER_MEDIUMDASHDOTDOT = 'mediumDashDotDot'
```

## `BORDER_MEDIUMDASHED`

```python
BORDER_MEDIUMDASHED = 'mediumDashed'
```

## `BORDER_NONE`

```python
BORDER_NONE = None
```

## `BORDER_SLANTDASHDOT`

```python
BORDER_SLANTDASHDOT = 'slantDashDot'
```

## `BORDER_THICK`

```python
BORDER_THICK = 'thick'
```

## `BORDER_THIN`

```python
BORDER_THIN = 'thin'
```

## `Border`

```python
Border(left = None, right = None, top = None, bottom = None, diagonal = None, diagonal_direction = None, vertical = None, horizontal = None, diagonalUp = False, diagonalDown = False, outline = True, start = None, end = None)
```

Bases: `Serialisable`

Border positioning for use in styles.

### `bottom`

```python
bottom = bottom
```

### `diagonal`

```python
diagonal = diagonal
```

### `diagonalDown`

```python
diagonalDown = diagonalDown
```

### `diagonalUp`

```python
diagonalUp = diagonalUp
```

### `diagonal_direction`

```python
diagonal_direction = diagonal_direction
```

### `end`

```python
end = end
```

### `horizontal`

```python
horizontal = horizontal
```

### `left`

```python
left = left
```

### `outline`

```python
outline = outline
```

### `right`

```python
right = right
```

### `start`

```python
start = start
```

### `tagname`

```python
tagname = 'border'
```

### `top`

```python
top = top
```

### `vertical`

```python
vertical = vertical
```

## `DEFAULT_BORDER`

```python
DEFAULT_BORDER = Border(left=Side(), right=Side(), top=Side(), bottom=Side(), diagonal=Side())
```

## `Side`

```python
Side(style = None, color = None, border_style = None)
```

Bases: `Serialisable`

Border options for use in styles.
Caution: if you do not specify a border_style, other attributes will
have no effect !

### `border_style`

```python
border_style = Alias('style')
```

### `color`

```python
color = color
```

### `style`

```python
style = style
```
