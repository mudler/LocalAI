<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.header_footer`

## `COLOR_PATTERN`

```python
COLOR_PATTERN = '&K(?P<color>[A-F0-9]{6})'
```

## `FONT_PATTERN`

```python
FONT_PATTERN = '&"(?P<font>.+)"'
```

## `FORMAT_REGEX`

```python
FORMAT_REGEX = re.compile('{0}|{1}|{2}'.format(FONT_PATTERN, COLOR_PATTERN, SIZE_REGEX))
```

## `HeaderFooter`

```python
HeaderFooter(differentOddEven = None, differentFirst = None, scaleWithDoc = None, alignWithMargins = None, oddHeader = None, oddFooter = None, evenHeader = None, evenFooter = None, firstHeader = None, firstFooter = None)
```

Bases: `Serialisable`

### `alignWithMargins`

```python
alignWithMargins = alignWithMargins
```

### `differentFirst`

```python
differentFirst = differentFirst
```

### `differentOddEven`

```python
differentOddEven = differentOddEven
```

### `evenFooter`

```python
evenFooter = evenFooter
```

### `evenHeader`

```python
evenHeader = evenHeader
```

### `firstFooter`

```python
firstFooter = firstFooter
```

### `firstHeader`

```python
firstHeader = firstHeader
```

### `oddFooter`

```python
oddFooter = oddFooter
```

### `oddHeader`

```python
oddHeader = oddHeader
```

### `scaleWithDoc`

```python
scaleWithDoc = scaleWithDoc
```

### `tagname`

```python
tagname = 'headerFooter'
```

## `HeaderFooterItem`

```python
HeaderFooterItem(left = None, right = None, center = None)
```

Bases: `Strict`

Header or footer item

### `center`

```python
center = center
```

### `centre`

```python
centre = Alias('center')
```

### `from_tree`

```python
from_tree(node)
```

### `left`

```python
left = left
```

### `right`

```python
right = right
```

### `to_tree`

```python
to_tree(tagname)
```

Return as XML node

## `SIZE_REGEX`

```python
SIZE_REGEX = '&(?P<size>\\d+\\s?)'
```
