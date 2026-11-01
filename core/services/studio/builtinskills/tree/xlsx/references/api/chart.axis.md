<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.chart.axis`

## `ChartLines`

```python
ChartLines(spPr = None)
```

Bases: `Serialisable`

### `graphicalProperties`

```python
graphicalProperties = Alias('spPr')
```

### `spPr`

```python
spPr = spPr
```

### `tagname`

```python
tagname = 'chartLines'
```

## `DateAxis`

```python
DateAxis(auto = None, lblOffset = None, baseTimeUnit = None, majorUnit = None, majorTimeUnit = None, minorUnit = None, minorTimeUnit = None, extLst = None, **kw)
```

Bases: `TextAxis`

### `auto`

```python
auto = auto
```

### `axId`

```python
axId = _BaseAxis.axId
```

### `axPos`

```python
axPos = _BaseAxis.axPos
```

### `baseTimeUnit`

```python
baseTimeUnit = baseTimeUnit
```

### `crossAx`

```python
crossAx = _BaseAxis.crossAx
```

### `crosses`

```python
crosses = _BaseAxis.crosses
```

### `crossesAt`

```python
crossesAt = _BaseAxis.crossesAt
```

### `delete`

```python
delete = _BaseAxis.delete
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `lblOffset`

```python
lblOffset = lblOffset
```

### `majorGridlines`

```python
majorGridlines = _BaseAxis.majorGridlines
```

### `majorTickMark`

```python
majorTickMark = _BaseAxis.majorTickMark
```

### `majorTimeUnit`

```python
majorTimeUnit = majorTimeUnit
```

### `majorUnit`

```python
majorUnit = majorUnit
```

### `minorGridlines`

```python
minorGridlines = _BaseAxis.minorGridlines
```

### `minorTickMark`

```python
minorTickMark = _BaseAxis.minorTickMark
```

### `minorTimeUnit`

```python
minorTimeUnit = minorTimeUnit
```

### `minorUnit`

```python
minorUnit = minorUnit
```

### `numFmt`

```python
numFmt = _BaseAxis.numFmt
```

### `scaling`

```python
scaling = _BaseAxis.scaling
```

### `spPr`

```python
spPr = _BaseAxis.spPr
```

### `tagname`

```python
tagname = 'dateAx'
```

### `tickLblPos`

```python
tickLblPos = _BaseAxis.tickLblPos
```

### `title`

```python
title = _BaseAxis.title
```

### `txPr`

```python
txPr = _BaseAxis.txPr
```

## `DisplayUnitsLabel`

```python
DisplayUnitsLabel(layout = None, tx = None, spPr = None, txPr = None)
```

Bases: `Serialisable`

### `graphicalProperties`

```python
graphicalProperties = Alias('spPr')
```

### `layout`

```python
layout = layout
```

### `spPr`

```python
spPr = spPr
```

### `tagname`

```python
tagname = 'dispUnitsLbl'
```

### `text`

```python
text = Alias('tx')
```

### `textPropertes`

```python
textPropertes = Alias('txPr')
```

### `tx`

```python
tx = tx
```

### `txPr`

```python
txPr = txPr
```

## `DisplayUnitsLabelList`

```python
DisplayUnitsLabelList(custUnit = None, builtInUnit = None, dispUnitsLbl = None, extLst = None)
```

Bases: `Serialisable`

### `builtInUnit`

```python
builtInUnit = builtInUnit
```

### `custUnit`

```python
custUnit = custUnit
```

### `dispUnitsLbl`

```python
dispUnitsLbl = dispUnitsLbl
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `tagname`

```python
tagname = 'dispUnits'
```

## `NumericAxis`

```python
NumericAxis(crossBetween = None, majorUnit = None, minorUnit = None, dispUnits = None, extLst = None, **kw)
```

Bases: `_BaseAxis`

### `axId`

```python
axId = _BaseAxis.axId
```

### `axPos`

```python
axPos = _BaseAxis.axPos
```

### `crossAx`

```python
crossAx = _BaseAxis.crossAx
```

### `crossBetween`

```python
crossBetween = crossBetween
```

### `crosses`

```python
crosses = _BaseAxis.crosses
```

### `crossesAt`

```python
crossesAt = _BaseAxis.crossesAt
```

### `delete`

```python
delete = _BaseAxis.delete
```

### `dispUnits`

```python
dispUnits = dispUnits
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `from_tree`

```python
from_tree(node)
```

Special case value axes with no gridlines

### `majorGridlines`

```python
majorGridlines = _BaseAxis.majorGridlines
```

### `majorTickMark`

```python
majorTickMark = _BaseAxis.majorTickMark
```

### `majorUnit`

```python
majorUnit = majorUnit
```

### `minorGridlines`

```python
minorGridlines = _BaseAxis.minorGridlines
```

### `minorTickMark`

```python
minorTickMark = _BaseAxis.minorTickMark
```

### `minorUnit`

```python
minorUnit = minorUnit
```

### `numFmt`

```python
numFmt = _BaseAxis.numFmt
```

### `scaling`

```python
scaling = _BaseAxis.scaling
```

### `spPr`

```python
spPr = _BaseAxis.spPr
```

### `tagname`

```python
tagname = 'valAx'
```

### `tickLblPos`

```python
tickLblPos = _BaseAxis.tickLblPos
```

### `title`

```python
title = _BaseAxis.title
```

### `txPr`

```python
txPr = _BaseAxis.txPr
```

## `Scaling`

```python
Scaling(logBase = None, orientation = 'minMax', max = None, min = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `logBase`

```python
logBase = logBase
```

### `max`

```python
max = max
```

### `min`

```python
min = min
```

### `orientation`

```python
orientation = orientation
```

### `tagname`

```python
tagname = 'scaling'
```

## `SeriesAxis`

```python
SeriesAxis(tickLblSkip = None, tickMarkSkip = None, extLst = None, **kw)
```

Bases: `_BaseAxis`

### `axId`

```python
axId = _BaseAxis.axId
```

### `axPos`

```python
axPos = _BaseAxis.axPos
```

### `crossAx`

```python
crossAx = _BaseAxis.crossAx
```

### `crosses`

```python
crosses = _BaseAxis.crosses
```

### `crossesAt`

```python
crossesAt = _BaseAxis.crossesAt
```

### `delete`

```python
delete = _BaseAxis.delete
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `majorGridlines`

```python
majorGridlines = _BaseAxis.majorGridlines
```

### `majorTickMark`

```python
majorTickMark = _BaseAxis.majorTickMark
```

### `minorGridlines`

```python
minorGridlines = _BaseAxis.minorGridlines
```

### `minorTickMark`

```python
minorTickMark = _BaseAxis.minorTickMark
```

### `numFmt`

```python
numFmt = _BaseAxis.numFmt
```

### `scaling`

```python
scaling = _BaseAxis.scaling
```

### `spPr`

```python
spPr = _BaseAxis.spPr
```

### `tagname`

```python
tagname = 'serAx'
```

### `tickLblPos`

```python
tickLblPos = _BaseAxis.tickLblPos
```

### `tickLblSkip`

```python
tickLblSkip = tickLblSkip
```

### `tickMarkSkip`

```python
tickMarkSkip = tickMarkSkip
```

### `title`

```python
title = _BaseAxis.title
```

### `txPr`

```python
txPr = _BaseAxis.txPr
```

## `TextAxis`

```python
TextAxis(auto = None, lblAlgn = None, lblOffset = 100, tickLblSkip = None, tickMarkSkip = None, noMultiLvlLbl = None, extLst = None, **kw)
```

Bases: `_BaseAxis`

### `auto`

```python
auto = auto
```

### `axId`

```python
axId = _BaseAxis.axId
```

### `axPos`

```python
axPos = _BaseAxis.axPos
```

### `crossAx`

```python
crossAx = _BaseAxis.crossAx
```

### `crosses`

```python
crosses = _BaseAxis.crosses
```

### `crossesAt`

```python
crossesAt = _BaseAxis.crossesAt
```

### `delete`

```python
delete = _BaseAxis.delete
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `lblAlgn`

```python
lblAlgn = lblAlgn
```

### `lblOffset`

```python
lblOffset = lblOffset
```

### `majorGridlines`

```python
majorGridlines = _BaseAxis.majorGridlines
```

### `majorTickMark`

```python
majorTickMark = _BaseAxis.majorTickMark
```

### `minorGridlines`

```python
minorGridlines = _BaseAxis.minorGridlines
```

### `minorTickMark`

```python
minorTickMark = _BaseAxis.minorTickMark
```

### `noMultiLvlLbl`

```python
noMultiLvlLbl = noMultiLvlLbl
```

### `numFmt`

```python
numFmt = _BaseAxis.numFmt
```

### `scaling`

```python
scaling = _BaseAxis.scaling
```

### `spPr`

```python
spPr = _BaseAxis.spPr
```

### `tagname`

```python
tagname = 'catAx'
```

### `tickLblPos`

```python
tickLblPos = _BaseAxis.tickLblPos
```

### `tickLblSkip`

```python
tickLblSkip = tickLblSkip
```

### `tickMarkSkip`

```python
tickMarkSkip = tickMarkSkip
```

### `title`

```python
title = _BaseAxis.title
```

### `txPr`

```python
txPr = _BaseAxis.txPr
```
