<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.pivot.table`

## `AutoSortScope`

```python
AutoSortScope(pivotArea = None)
```

Bases: `Serialisable`

### `pivotArea`

```python
pivotArea = pivotArea
```

## `ChartFormat`

```python
ChartFormat(chart = None, format = None, series = None, pivotArea = None)
```

Bases: `Serialisable`

### `chart`

```python
chart = chart
```

### `format`

```python
format = format
```

### `pivotArea`

```python
pivotArea = pivotArea
```

### `series`

```python
series = series
```

### `tagname`

```python
tagname = 'chartFormat'
```

## `ColHierarchiesUsage`

```python
ColHierarchiesUsage(count = None, colHierarchyUsage = ())
```

Bases: `Serialisable`

### `colHierarchyUsage`

```python
colHierarchyUsage = colHierarchyUsage
```

### `count`

```python
count
```

### `tagname`

```python
tagname = 'colHierarchiesUsage'
```

## `ConditionalFormat`

```python
ConditionalFormat(scope = 'selection', type = None, priority = None, pivotAreas = (), extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = extLst
```

### `pivotAreas`

```python
pivotAreas = pivotAreas
```

### `priority`

```python
priority = priority
```

### `scope`

```python
scope = scope
```

### `tagname`

```python
tagname = 'conditionalFormat'
```

### `type`

```python
type = type
```

## `ConditionalFormatList`

```python
ConditionalFormatList(conditionalFormat = (), count = None)
```

Bases: `Serialisable`

### `by_priority`

```python
by_priority()
```

Return a dictionary of format objects keyed by (field id and format property).
This can be used to map the formats to field but also to dedupe to match
worksheet definitions which are grouped by cell range

### `conditionalFormat`

```python
conditionalFormat = conditionalFormat
```

### `count`

```python
count
```

### `tagname`

```python
tagname = 'conditionalFormats'
```

### `to_tree`

```python
to_tree(tagname = None)
```

## `DataField`

```python
DataField(name = None, fld = None, subtotal = 'sum', showDataAs = 'normal', baseField = -1, baseItem = 1048832, numFmtId = None, extLst = None)
```

Bases: `Serialisable`

### `baseField`

```python
baseField = baseField
```

### `baseItem`

```python
baseItem = baseItem
```

### `extLst`

```python
extLst = extLst
```

### `fld`

```python
fld = fld
```

### `name`

```python
name = name
```

### `numFmtId`

```python
numFmtId = numFmtId
```

### `showDataAs`

```python
showDataAs = showDataAs
```

### `subtotal`

```python
subtotal = subtotal
```

### `tagname`

```python
tagname = 'dataField'
```

## `FieldItem`

```python
FieldItem(n = None, t = 'data', h = None, s = None, sd = True, f = None, m = None, c = None, x = None, d = None, e = None)
```

Bases: `Serialisable`

### `c`

```python
c = c
```

### `d`

```python
d = d
```

### `e`

```python
e = e
```

### `f`

```python
f = f
```

### `h`

```python
h = h
```

### `m`

```python
m = m
```

### `n`

```python
n = n
```

### `s`

```python
s = s
```

### `sd`

```python
sd = sd
```

### `t`

```python
t = t
```

### `tagname`

```python
tagname = 'item'
```

### `x`

```python
x = x
```

## `Format`

```python
Format(action = 'formatting', dxfId = None, pivotArea = None, extLst = None)
```

Bases: `Serialisable`

### `action`

```python
action = action
```

### `dxfId`

```python
dxfId = dxfId
```

### `extLst`

```python
extLst = extLst
```

### `pivotArea`

```python
pivotArea = pivotArea
```

### `tagname`

```python
tagname = 'format'
```

## `HierarchyUsage`

```python
HierarchyUsage(hierarchyUsage = None)
```

Bases: `Serialisable`

### `hierarchyUsage`

```python
hierarchyUsage = hierarchyUsage
```

### `tagname`

```python
tagname = 'hierarchyUsage'
```

## `Location`

```python
Location(ref = None, firstHeaderRow = None, firstDataRow = None, firstDataCol = None, rowPageCount = None, colPageCount = None)
```

Bases: `Serialisable`

### `colPageCount`

```python
colPageCount = colPageCount
```

### `firstDataCol`

```python
firstDataCol = firstDataCol
```

### `firstDataRow`

```python
firstDataRow = firstDataRow
```

### `firstHeaderRow`

```python
firstHeaderRow = firstHeaderRow
```

### `ref`

```python
ref = ref
```

### `rowPageCount`

```python
rowPageCount = rowPageCount
```

### `tagname`

```python
tagname = 'location'
```

## `MemberList`

```python
MemberList(count = None, level = None, member = ())
```

Bases: `Serialisable`

### `count`

```python
count
```

### `level`

```python
level = level
```

### `member`

```python
member = member
```

### `tagname`

```python
tagname = 'members'
```

## `MemberProperty`

```python
MemberProperty(name = None, showCell = None, showTip = None, showAsCaption = None, nameLen = None, pPos = None, pLen = None, level = None, field = None)
```

Bases: `Serialisable`

### `field`

```python
field = field
```

### `level`

```python
level = level
```

### `name`

```python
name = name
```

### `nameLen`

```python
nameLen = nameLen
```

### `pLen`

```python
pLen = pLen
```

### `pPos`

```python
pPos = pPos
```

### `showAsCaption`

```python
showAsCaption = showAsCaption
```

### `showCell`

```python
showCell = showCell
```

### `showTip`

```python
showTip = showTip
```

### `tagname`

```python
tagname = 'mps'
```

## `PageField`

```python
PageField(fld = None, item = None, hier = None, name = None, cap = None, extLst = None)
```

Bases: `Serialisable`

### `cap`

```python
cap = cap
```

### `extLst`

```python
extLst = extLst
```

### `fld`

```python
fld = fld
```

### `hier`

```python
hier = hier
```

### `item`

```python
item = item
```

### `name`

```python
name = name
```

### `tagname`

```python
tagname = 'pageField'
```

## `PivotArea`

```python
PivotArea(references = (), extLst = None, field = None, type = 'normal', dataOnly = True, labelOnly = None, grandRow = None, grandCol = None, cacheIndex = None, outline = True, offset = None, collapsedLevelsAreSubtotals = None, axis = None, fieldPosition = None)
```

Bases: `Serialisable`

### `axis`

```python
axis = axis
```

### `cacheIndex`

```python
cacheIndex = cacheIndex
```

### `collapsedLevelsAreSubtotals`

```python
collapsedLevelsAreSubtotals = collapsedLevelsAreSubtotals
```

### `dataOnly`

```python
dataOnly = dataOnly
```

### `extLst`

```python
extLst = extLst
```

### `field`

```python
field = field
```

### `fieldPosition`

```python
fieldPosition = fieldPosition
```

### `grandCol`

```python
grandCol = grandCol
```

### `grandRow`

```python
grandRow = grandRow
```

### `labelOnly`

```python
labelOnly = labelOnly
```

### `offset`

```python
offset = offset
```

### `outline`

```python
outline = outline
```

### `references`

```python
references = references
```

### `tagname`

```python
tagname = 'pivotArea'
```

### `type`

```python
type = type
```

## `PivotField`

```python
PivotField(items = (), autoSortScope = None, name = None, axis = None, dataField = None, subtotalCaption = None, showDropDowns = True, hiddenLevel = None, uniqueMemberProperty = None, compact = True, allDrilled = None, numFmtId = None, outline = True, subtotalTop = True, dragToRow = True, dragToCol = True, multipleItemSelectionAllowed = None, dragToPage = True, dragToData = True, dragOff = True, showAll = True, insertBlankRow = None, serverField = None, insertPageBreak = None, autoShow = None, topAutoShow = True, hideNewItems = None, measureFilter = None, includeNewItemsInFilter = None, itemPageCount = 10, sortType = 'manual', dataSourceSort = None, nonAutoSortDefault = None, rankBy = None, defaultSubtotal = True, sumSubtotal = None, countASubtotal = None, avgSubtotal = None, maxSubtotal = None, minSubtotal = None, productSubtotal = None, countSubtotal = None, stdDevSubtotal = None, stdDevPSubtotal = None, varSubtotal = None, varPSubtotal = None, showPropCell = None, showPropTip = None, showPropAsCaption = None, defaultAttributeDrillState = None, extLst = None)
```

Bases: `Serialisable`

### `allDrilled`

```python
allDrilled = allDrilled
```

### `autoShow`

```python
autoShow = autoShow
```

### `autoSortScope`

```python
autoSortScope = autoSortScope
```

### `avgSubtotal`

```python
avgSubtotal = avgSubtotal
```

### `axis`

```python
axis = axis
```

### `compact`

```python
compact = compact
```

### `countASubtotal`

```python
countASubtotal = countASubtotal
```

### `countSubtotal`

```python
countSubtotal = countSubtotal
```

### `dataField`

```python
dataField = dataField
```

### `dataSourceSort`

```python
dataSourceSort = dataSourceSort
```

### `defaultAttributeDrillState`

```python
defaultAttributeDrillState = defaultAttributeDrillState
```

### `defaultSubtotal`

```python
defaultSubtotal = defaultSubtotal
```

### `dragOff`

```python
dragOff = dragOff
```

### `dragToCol`

```python
dragToCol = dragToCol
```

### `dragToData`

```python
dragToData = dragToData
```

### `dragToPage`

```python
dragToPage = dragToPage
```

### `dragToRow`

```python
dragToRow = dragToRow
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `hiddenLevel`

```python
hiddenLevel = hiddenLevel
```

### `hideNewItems`

```python
hideNewItems = hideNewItems
```

### `includeNewItemsInFilter`

```python
includeNewItemsInFilter = includeNewItemsInFilter
```

### `insertBlankRow`

```python
insertBlankRow = insertBlankRow
```

### `insertPageBreak`

```python
insertPageBreak = insertPageBreak
```

### `itemPageCount`

```python
itemPageCount = itemPageCount
```

### `items`

```python
items = items
```

### `maxSubtotal`

```python
maxSubtotal = maxSubtotal
```

### `measureFilter`

```python
measureFilter = measureFilter
```

### `minSubtotal`

```python
minSubtotal = minSubtotal
```

### `multipleItemSelectionAllowed`

```python
multipleItemSelectionAllowed = multipleItemSelectionAllowed
```

### `name`

```python
name = name
```

### `nonAutoSortDefault`

```python
nonAutoSortDefault = nonAutoSortDefault
```

### `numFmtId`

```python
numFmtId = numFmtId
```

### `outline`

```python
outline = outline
```

### `productSubtotal`

```python
productSubtotal = productSubtotal
```

### `rankBy`

```python
rankBy = rankBy
```

### `serverField`

```python
serverField = serverField
```

### `showAll`

```python
showAll = showAll
```

### `showDropDowns`

```python
showDropDowns = showDropDowns
```

### `showPropAsCaption`

```python
showPropAsCaption = showPropAsCaption
```

### `showPropCell`

```python
showPropCell = showPropCell
```

### `showPropTip`

```python
showPropTip = showPropTip
```

### `sortType`

```python
sortType = sortType
```

### `stdDevPSubtotal`

```python
stdDevPSubtotal = stdDevPSubtotal
```

### `stdDevSubtotal`

```python
stdDevSubtotal = stdDevSubtotal
```

### `subtotalCaption`

```python
subtotalCaption = subtotalCaption
```

### `subtotalTop`

```python
subtotalTop = subtotalTop
```

### `sumSubtotal`

```python
sumSubtotal = sumSubtotal
```

### `tagname`

```python
tagname = 'pivotField'
```

### `topAutoShow`

```python
topAutoShow = topAutoShow
```

### `uniqueMemberProperty`

```python
uniqueMemberProperty = uniqueMemberProperty
```

### `varPSubtotal`

```python
varPSubtotal = varPSubtotal
```

### `varSubtotal`

```python
varSubtotal = varSubtotal
```

## `PivotFilter`

```python
PivotFilter(fld = None, mpFld = None, type = None, evalOrder = None, id = None, iMeasureHier = None, iMeasureFld = None, name = None, description = None, stringValue1 = None, stringValue2 = None, autoFilter = None, extLst = None)
```

Bases: `Serialisable`

### `autoFilter`

```python
autoFilter = autoFilter
```

### `description`

```python
description = description
```

### `evalOrder`

```python
evalOrder = evalOrder
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `fld`

```python
fld = fld
```

### `iMeasureFld`

```python
iMeasureFld = iMeasureFld
```

### `iMeasureHier`

```python
iMeasureHier = iMeasureHier
```

### `id`

```python
id = id
```

### `mpFld`

```python
mpFld = mpFld
```

### `name`

```python
name = name
```

### `stringValue1`

```python
stringValue1 = stringValue1
```

### `stringValue2`

```python
stringValue2 = stringValue2
```

### `tagname`

```python
tagname = 'filter'
```

### `type`

```python
type = type
```

## `PivotFilters`

```python
PivotFilters(count = None, filter = None)
```

Bases: `Serialisable`

### `count`

```python
count = Integer()
```

### `filter`

```python
filter = filter
```

## `PivotHierarchy`

```python
PivotHierarchy(outline = None, multipleItemSelectionAllowed = None, subtotalTop = None, showInFieldList = None, dragToRow = None, dragToCol = None, dragToPage = None, dragToData = None, dragOff = None, includeNewItemsInFilter = None, caption = None, mps = (), members = None, extLst = None)
```

Bases: `Serialisable`

### `caption`

```python
caption = caption
```

### `dragOff`

```python
dragOff = dragOff
```

### `dragToCol`

```python
dragToCol = dragToCol
```

### `dragToData`

```python
dragToData = dragToData
```

### `dragToPage`

```python
dragToPage = dragToPage
```

### `dragToRow`

```python
dragToRow = dragToRow
```

### `extLst`

```python
extLst = extLst
```

### `includeNewItemsInFilter`

```python
includeNewItemsInFilter = includeNewItemsInFilter
```

### `members`

```python
members = members
```

### `mps`

```python
mps = mps
```

### `multipleItemSelectionAllowed`

```python
multipleItemSelectionAllowed = multipleItemSelectionAllowed
```

### `outline`

```python
outline = outline
```

### `showInFieldList`

```python
showInFieldList = showInFieldList
```

### `subtotalTop`

```python
subtotalTop = subtotalTop
```

### `tagname`

```python
tagname = 'pivotHierarchy'
```

## `PivotTableStyle`

```python
PivotTableStyle(name = None, showRowHeaders = None, showColHeaders = None, showRowStripes = None, showColStripes = None, showLastColumn = None)
```

Bases: `Serialisable`

### `name`

```python
name = name
```

### `showColHeaders`

```python
showColHeaders = showColHeaders
```

### `showColStripes`

```python
showColStripes = showColStripes
```

### `showLastColumn`

```python
showLastColumn = showLastColumn
```

### `showRowHeaders`

```python
showRowHeaders = showRowHeaders
```

### `showRowStripes`

```python
showRowStripes = showRowStripes
```

### `tagname`

```python
tagname = 'pivotTableStyleInfo'
```

## `Reference`

```python
Reference(field = None, count = None, selected = None, byPosition = None, relative = None, defaultSubtotal = None, sumSubtotal = None, countASubtotal = None, avgSubtotal = None, maxSubtotal = None, minSubtotal = None, productSubtotal = None, countSubtotal = None, stdDevSubtotal = None, stdDevPSubtotal = None, varSubtotal = None, varPSubtotal = None, x = (), extLst = None)
```

Bases: `Serialisable`

### `avgSubtotal`

```python
avgSubtotal = avgSubtotal
```

### `byPosition`

```python
byPosition = byPosition
```

### `count`

```python
count
```

### `countASubtotal`

```python
countASubtotal = countASubtotal
```

### `countSubtotal`

```python
countSubtotal = countSubtotal
```

### `defaultSubtotal`

```python
defaultSubtotal = defaultSubtotal
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `field`

```python
field = field
```

### `maxSubtotal`

```python
maxSubtotal = maxSubtotal
```

### `minSubtotal`

```python
minSubtotal = minSubtotal
```

### `productSubtotal`

```python
productSubtotal = productSubtotal
```

### `relative`

```python
relative = relative
```

### `selected`

```python
selected = selected
```

### `stdDevPSubtotal`

```python
stdDevPSubtotal = stdDevPSubtotal
```

### `stdDevSubtotal`

```python
stdDevSubtotal = stdDevSubtotal
```

### `sumSubtotal`

```python
sumSubtotal = sumSubtotal
```

### `tagname`

```python
tagname = 'reference'
```

### `varPSubtotal`

```python
varPSubtotal = varPSubtotal
```

### `varSubtotal`

```python
varSubtotal = varSubtotal
```

### `x`

```python
x = x
```

## `RowColField`

```python
RowColField(x = None)
```

Bases: `Serialisable`

### `tagname`

```python
tagname = 'field'
```

### `x`

```python
x = x
```

## `RowColItem`

```python
RowColItem(t = 'data', r = 0, i = 0, x = ())
```

Bases: `Serialisable`

### `i`

```python
i = i
```

### `r`

```python
r = r
```

### `t`

```python
t = t
```

### `tagname`

```python
tagname = 'i'
```

### `x`

```python
x = x
```

## `RowHierarchiesUsage`

```python
RowHierarchiesUsage(count = None, rowHierarchyUsage = ())
```

Bases: `Serialisable`

### `count`

```python
count
```

### `rowHierarchyUsage`

```python
rowHierarchyUsage = rowHierarchyUsage
```

### `tagname`

```python
tagname = 'rowHierarchiesUsage'
```

## `TableDefinition`

```python
TableDefinition(name = None, cacheId = None, dataOnRows = False, dataPosition = None, dataCaption = None, grandTotalCaption = None, errorCaption = None, showError = False, missingCaption = None, showMissing = True, pageStyle = None, pivotTableStyle = None, vacatedStyle = None, tag = None, updatedVersion = 0, minRefreshableVersion = 0, asteriskTotals = False, showItems = True, editData = False, disableFieldList = False, showCalcMbrs = True, visualTotals = True, showMultipleLabel = True, showDataDropDown = True, showDrill = True, printDrill = False, showMemberPropertyTips = True, showDataTips = True, enableWizard = True, enableDrill = True, enableFieldProperties = True, preserveFormatting = True, useAutoFormatting = False, pageWrap = 0, pageOverThenDown = False, subtotalHiddenItems = False, rowGrandTotals = True, colGrandTotals = True, fieldPrintTitles = False, itemPrintTitles = False, mergeItem = False, showDropZones = True, createdVersion = 0, indent = 1, showEmptyRow = False, showEmptyCol = False, showHeaders = True, compact = True, outline = False, outlineData = False, compactData = True, published = False, gridDropZones = False, immersive = True, multipleFieldFilters = None, chartFormat = 0, rowHeaderCaption = None, colHeaderCaption = None, fieldListSortAscending = None, mdxSubqueries = None, customListSort = None, autoFormatId = None, applyNumberFormats = False, applyBorderFormats = False, applyFontFormats = False, applyPatternFormats = False, applyAlignmentFormats = False, applyWidthHeightFormats = False, location = None, pivotFields = (), rowFields = (), rowItems = (), colFields = (), colItems = (), pageFields = (), dataFields = (), formats = (), conditionalFormats = None, chartFormats = (), pivotHierarchies = (), pivotTableStyleInfo = None, filters = (), rowHierarchiesUsage = None, colHierarchiesUsage = None, extLst = None, id = None)
```

Bases: `Serialisable`

### `applyAlignmentFormats`

```python
applyAlignmentFormats = applyAlignmentFormats
```

### `applyBorderFormats`

```python
applyBorderFormats = applyBorderFormats
```

### `applyFontFormats`

```python
applyFontFormats = applyFontFormats
```

### `applyNumberFormats`

```python
applyNumberFormats = applyNumberFormats
```

### `applyPatternFormats`

```python
applyPatternFormats = applyPatternFormats
```

### `applyWidthHeightFormats`

```python
applyWidthHeightFormats = applyWidthHeightFormats
```

### `asteriskTotals`

```python
asteriskTotals = asteriskTotals
```

### `autoFormatId`

```python
autoFormatId = autoFormatId
```

### `cache`

```python
cache = None
```

### `cacheId`

```python
cacheId = cacheId
```

### `chartFormat`

```python
chartFormat = chartFormat
```

### `chartFormats`

```python
chartFormats = chartFormats
```

### `colFields`

```python
colFields = colFields
```

### `colGrandTotals`

```python
colGrandTotals = colGrandTotals
```

### `colHeaderCaption`

```python
colHeaderCaption = colHeaderCaption
```

### `colHierarchiesUsage`

```python
colHierarchiesUsage = colHierarchiesUsage
```

### `colItems`

```python
colItems = colItems
```

### `compact`

```python
compact = compact
```

### `compactData`

```python
compactData = compactData
```

### `conditionalFormats`

```python
conditionalFormats = None
```

### `createdVersion`

```python
createdVersion = createdVersion
```

### `customListSort`

```python
customListSort = customListSort
```

### `dataCaption`

```python
dataCaption = dataCaption
```

### `dataFields`

```python
dataFields = dataFields
```

### `dataOnRows`

```python
dataOnRows = dataOnRows
```

### `dataPosition`

```python
dataPosition = dataPosition
```

### `disableFieldList`

```python
disableFieldList = disableFieldList
```

### `editData`

```python
editData = editData
```

### `enableDrill`

```python
enableDrill = enableDrill
```

### `enableFieldProperties`

```python
enableFieldProperties = enableFieldProperties
```

### `enableWizard`

```python
enableWizard = enableWizard
```

### `errorCaption`

```python
errorCaption = errorCaption
```

### `extLst`

```python
extLst = extLst
```

### `fieldListSortAscending`

```python
fieldListSortAscending = fieldListSortAscending
```

### `fieldPrintTitles`

```python
fieldPrintTitles = fieldPrintTitles
```

### `filters`

```python
filters = filters
```

### `formats`

```python
formats = formats
```

### `formatted_fields`

```python
formatted_fields()
```

Map fields to associated conditional formats by priority

### `grandTotalCaption`

```python
grandTotalCaption = grandTotalCaption
```

### `gridDropZones`

```python
gridDropZones = gridDropZones
```

### `id`

```python
id = id
```

### `immersive`

```python
immersive = immersive
```

### `indent`

```python
indent = indent
```

### `itemPrintTitles`

```python
itemPrintTitles = itemPrintTitles
```

### `location`

```python
location = location
```

### `mdxSubqueries`

```python
mdxSubqueries = mdxSubqueries
```

### `mergeItem`

```python
mergeItem = mergeItem
```

### `mime_type`

```python
mime_type = 'application/vnd.openxmlformats-officedocument.spreadsheetml.pivotTable+xml'
```

### `minRefreshableVersion`

```python
minRefreshableVersion = minRefreshableVersion
```

### `missingCaption`

```python
missingCaption = missingCaption
```

### `multipleFieldFilters`

```python
multipleFieldFilters = multipleFieldFilters
```

### `name`

```python
name = name
```

### `outline`

```python
outline = outline
```

### `outlineData`

```python
outlineData = outlineData
```

### `pageFields`

```python
pageFields = pageFields
```

### `pageOverThenDown`

```python
pageOverThenDown = pageOverThenDown
```

### `pageStyle`

```python
pageStyle = pageStyle
```

### `pageWrap`

```python
pageWrap = pageWrap
```

### `path`

```python
path
```

### `pivotFields`

```python
pivotFields = pivotFields
```

### `pivotHierarchies`

```python
pivotHierarchies = pivotHierarchies
```

### `pivotTableStyle`

```python
pivotTableStyle = pivotTableStyle
```

### `pivotTableStyleInfo`

```python
pivotTableStyleInfo = pivotTableStyleInfo
```

### `preserveFormatting`

```python
preserveFormatting = preserveFormatting
```

### `printDrill`

```python
printDrill = printDrill
```

### `published`

```python
published = published
```

### `rel_type`

```python
rel_type = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships/pivotTable'
```

### `rowFields`

```python
rowFields = rowFields
```

### `rowGrandTotals`

```python
rowGrandTotals = rowGrandTotals
```

### `rowHeaderCaption`

```python
rowHeaderCaption = rowHeaderCaption
```

### `rowHierarchiesUsage`

```python
rowHierarchiesUsage = rowHierarchiesUsage
```

### `rowItems`

```python
rowItems = rowItems
```

### `showCalcMbrs`

```python
showCalcMbrs = showCalcMbrs
```

### `showDataDropDown`

```python
showDataDropDown = showDataDropDown
```

### `showDataTips`

```python
showDataTips = showDataTips
```

### `showDrill`

```python
showDrill = showDrill
```

### `showDropZones`

```python
showDropZones = showDropZones
```

### `showEmptyCol`

```python
showEmptyCol = showEmptyCol
```

### `showEmptyRow`

```python
showEmptyRow = showEmptyRow
```

### `showError`

```python
showError = showError
```

### `showHeaders`

```python
showHeaders = showHeaders
```

### `showItems`

```python
showItems = showItems
```

### `showMemberPropertyTips`

```python
showMemberPropertyTips = showMemberPropertyTips
```

### `showMissing`

```python
showMissing = showMissing
```

### `showMultipleLabel`

```python
showMultipleLabel = showMultipleLabel
```

### `subtotalHiddenItems`

```python
subtotalHiddenItems = subtotalHiddenItems
```

### `summary`

```python
summary
```

Provide a simplified summary of the table

### `tag`

```python
tag = tag
```

### `tagname`

```python
tagname = 'pivotTableDefinition'
```

### `to_tree`

```python
to_tree()
```

### `updatedVersion`

```python
updatedVersion = updatedVersion
```

### `useAutoFormatting`

```python
useAutoFormatting = useAutoFormatting
```

### `vacatedStyle`

```python
vacatedStyle = vacatedStyle
```

### `visualTotals`

```python
visualTotals = visualTotals
```
