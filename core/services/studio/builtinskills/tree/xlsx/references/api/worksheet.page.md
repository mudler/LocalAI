<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.page`

## `PageMargins`

```python
PageMargins(left = 0.75, right = 0.75, top = 1, bottom = 1, header = 0.5, footer = 0.5)
```

Bases: `Serialisable`

Information about page margins for view/print layouts.
Standard values (in inches)
left, right = 0.75
top, bottom = 1
header, footer = 0.5

### `bottom`

```python
bottom = bottom
```

### `footer`

```python
footer = footer
```

### `header`

```python
header = header
```

### `left`

```python
left = left
```

### `right`

```python
right = right
```

### `tagname`

```python
tagname = 'pageMargins'
```

### `top`

```python
top = top
```

## `PrintOptions`

```python
PrintOptions(horizontalCentered = None, verticalCentered = None, headings = None, gridLines = None, gridLinesSet = None)
```

Bases: `Serialisable`

Worksheet print options

### `gridLines`

```python
gridLines = gridLines
```

### `gridLinesSet`

```python
gridLinesSet = gridLinesSet
```

### `headings`

```python
headings = headings
```

### `horizontalCentered`

```python
horizontalCentered = horizontalCentered
```

### `tagname`

```python
tagname = 'printOptions'
```

### `verticalCentered`

```python
verticalCentered = verticalCentered
```

## `PrintPageSetup`

```python
PrintPageSetup(worksheet = None, orientation = None, paperSize = None, scale = None, fitToHeight = None, fitToWidth = None, firstPageNumber = None, useFirstPageNumber = None, paperHeight = None, paperWidth = None, pageOrder = None, usePrinterDefaults = None, blackAndWhite = None, draft = None, cellComments = None, errors = None, horizontalDpi = None, verticalDpi = None, copies = None, id = None)
```

Bases: `Serialisable`

Worksheet print page setup

### `autoPageBreaks`

```python
autoPageBreaks
```

### `blackAndWhite`

```python
blackAndWhite = blackAndWhite
```

### `cellComments`

```python
cellComments = cellComments
```

### `copies`

```python
copies = copies
```

### `draft`

```python
draft = draft
```

### `errors`

```python
errors = errors
```

### `firstPageNumber`

```python
firstPageNumber = firstPageNumber
```

### `fitToHeight`

```python
fitToHeight = fitToHeight
```

### `fitToPage`

```python
fitToPage
```

### `fitToWidth`

```python
fitToWidth = fitToWidth
```

### `from_tree`

```python
from_tree(node)
```

### `horizontalDpi`

```python
horizontalDpi = horizontalDpi
```

### `id`

```python
id = id
```

### `orientation`

```python
orientation = orientation
```

### `pageOrder`

```python
pageOrder = pageOrder
```

### `paperHeight`

```python
paperHeight = paperHeight
```

### `paperSize`

```python
paperSize = paperSize
```

### `paperWidth`

```python
paperWidth = paperWidth
```

### `scale`

```python
scale = scale
```

### `sheet_properties`

```python
sheet_properties
```

Proxy property

### `tagname`

```python
tagname = 'pageSetup'
```

### `useFirstPageNumber`

```python
useFirstPageNumber = useFirstPageNumber
```

### `usePrinterDefaults`

```python
usePrinterDefaults = usePrinterDefaults
```

### `verticalDpi`

```python
verticalDpi = verticalDpi
```
