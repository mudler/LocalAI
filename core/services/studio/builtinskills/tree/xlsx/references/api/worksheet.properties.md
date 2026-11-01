<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.worksheet.properties`

Worksheet Properties

## `Outline`

```python
Outline(applyStyles = None, summaryBelow = None, summaryRight = None, showOutlineSymbols = None)
```

Bases: `Serialisable`

### `applyStyles`

```python
applyStyles = applyStyles
```

### `showOutlineSymbols`

```python
showOutlineSymbols = showOutlineSymbols
```

### `summaryBelow`

```python
summaryBelow = summaryBelow
```

### `summaryRight`

```python
summaryRight = summaryRight
```

### `tagname`

```python
tagname = 'outlinePr'
```

## `PageSetupProperties`

```python
PageSetupProperties(autoPageBreaks = None, fitToPage = None)
```

Bases: `Serialisable`

### `autoPageBreaks`

```python
autoPageBreaks = autoPageBreaks
```

### `fitToPage`

```python
fitToPage = fitToPage
```

### `tagname`

```python
tagname = 'pageSetUpPr'
```

## `WorksheetProperties`

```python
WorksheetProperties(codeName = None, enableFormatConditionsCalculation = None, filterMode = None, published = None, syncHorizontal = None, syncRef = None, syncVertical = None, transitionEvaluation = None, transitionEntry = None, tabColor = None, outlinePr = None, pageSetUpPr = None)
```

Bases: `Serialisable`

Attributes

### `codeName`

```python
codeName = codeName
```

### `enableFormatConditionsCalculation`

```python
enableFormatConditionsCalculation = enableFormatConditionsCalculation
```

### `filterMode`

```python
filterMode = filterMode
```

### `outlinePr`

```python
outlinePr = Typed(expected_type=Outline, allow_none=True)
```

### `pageSetUpPr`

```python
pageSetUpPr = pageSetUpPr
```

### `published`

```python
published = published
```

### `syncHorizontal`

```python
syncHorizontal = syncHorizontal
```

### `syncRef`

```python
syncRef = syncRef
```

### `syncVertical`

```python
syncVertical = syncVertical
```

### `tabColor`

```python
tabColor = tabColor
```

### `tagname`

```python
tagname = 'sheetPr'
```

### `transitionEntry`

```python
transitionEntry = transitionEntry
```

Elements

### `transitionEvaluation`

```python
transitionEvaluation = transitionEvaluation
```
