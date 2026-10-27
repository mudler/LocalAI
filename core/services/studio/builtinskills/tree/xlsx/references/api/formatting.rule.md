<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.formatting.rule`

## `CellIsRule`

```python
CellIsRule(operator = None, formula = None, stopIfTrue = None, font = None, border = None, fill = None)
```

Conditional formatting rule based on cell contents.

## `ColorScale`

```python
ColorScale(cfvo = None, color = None)
```

Bases: `RuleType`

### `cfvo`

```python
cfvo = cfvo
```

### `color`

```python
color = color
```

### `tagname`

```python
tagname = 'colorScale'
```

## `ColorScaleRule`

```python
ColorScaleRule(start_type = None, start_value = None, start_color = None, mid_type = None, mid_value = None, mid_color = None, end_type = None, end_value = None, end_color = None)
```

Backwards compatibility

## `DataBar`

```python
DataBar(minLength = None, maxLength = None, showValue = None, cfvo = None, color = None)
```

Bases: `RuleType`

### `cfvo`

```python
cfvo = cfvo
```

### `color`

```python
color = color
```

### `maxLength`

```python
maxLength = maxLength
```

### `minLength`

```python
minLength = minLength
```

### `showValue`

```python
showValue = showValue
```

### `tagname`

```python
tagname = 'dataBar'
```

## `DataBarRule`

```python
DataBarRule(start_type = None, start_value = None, end_type = None, end_value = None, color = None, showValue = None, minLength = None, maxLength = None)
```

## `FormatObject`

```python
FormatObject(type, val = None, gte = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `gte`

```python
gte = gte
```

### `tagname`

```python
tagname = 'cfvo'
```

### `type`

```python
type = type
```

### `val`

```python
val = val
```

## `FormulaRule`

```python
FormulaRule(formula = None, stopIfTrue = None, font = None, border = None, fill = None)
```

Conditional formatting with custom differential style

## `IconSet`

```python
IconSet(iconSet = None, showValue = None, percent = None, reverse = None, cfvo = None)
```

Bases: `RuleType`

### `cfvo`

```python
cfvo = cfvo
```

### `iconSet`

```python
iconSet = iconSet
```

### `percent`

```python
percent = percent
```

### `reverse`

```python
reverse = reverse
```

### `showValue`

```python
showValue = showValue
```

### `tagname`

```python
tagname = 'iconSet'
```

## `IconSetRule`

```python
IconSetRule(icon_style = None, type = None, values = None, showValue = None, percent = None, reverse = None)
```

Convenience function for creating icon set rules

## `Rule`

```python
Rule(type, dxfId = None, priority = 0, stopIfTrue = None, aboveAverage = None, percent = None, bottom = None, operator = None, text = None, timePeriod = None, rank = None, stdDev = None, equalAverage = None, formula = (), colorScale = None, dataBar = None, iconSet = None, extLst = None, dxf = None)
```

Bases: `Serialisable`

### `aboveAverage`

```python
aboveAverage = aboveAverage
```

### `bottom`

```python
bottom = bottom
```

### `colorScale`

```python
colorScale = colorScale
```

### `dataBar`

```python
dataBar = dataBar
```

### `dxf`

```python
dxf = dxf
```

### `dxfId`

```python
dxfId = dxfId
```

### `equalAverage`

```python
equalAverage = equalAverage
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `formula`

```python
formula = formula
```

### `iconSet`

```python
iconSet = iconSet
```

### `operator`

```python
operator = operator
```

### `percent`

```python
percent = percent
```

### `priority`

```python
priority = priority
```

### `rank`

```python
rank = rank
```

### `stdDev`

```python
stdDev = stdDev
```

### `stopIfTrue`

```python
stopIfTrue = stopIfTrue
```

### `tagname`

```python
tagname = 'cfRule'
```

### `text`

```python
text = text
```

### `timePeriod`

```python
timePeriod = timePeriod
```

### `type`

```python
type = type
```

## `RuleType`

Bases: `Serialisable`

### `cfvo`

```python
cfvo = Sequence(expected_type=FormatObject)
```

## `ValueDescriptor`

Bases: `Float`

Expected type depends upon type attribute of parent :-(

Most values should be numeric BUT they can also be cell references
