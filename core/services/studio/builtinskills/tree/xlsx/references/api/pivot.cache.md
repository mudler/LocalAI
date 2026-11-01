<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.pivot.cache`

## `CacheDefinition`

```python
CacheDefinition(invalid = None, saveData = None, refreshOnLoad = None, optimizeMemory = None, enableRefresh = None, refreshedBy = None, refreshedDate = None, refreshedDateIso = None, backgroundQuery = None, missingItemsLimit = None, createdVersion = None, refreshedVersion = None, minRefreshableVersion = None, recordCount = None, upgradeOnRefresh = None, tupleCache = None, supportSubquery = None, supportAdvancedDrill = None, cacheSource = None, cacheFields = (), cacheHierarchies = (), kpis = (), calculatedItems = (), calculatedMembers = (), dimensions = (), measureGroups = (), maps = (), extLst = None, id = None)
```

Bases: `Serialisable`

### `backgroundQuery`

```python
backgroundQuery = backgroundQuery
```

### `cacheFields`

```python
cacheFields = cacheFields
```

### `cacheHierarchies`

```python
cacheHierarchies = cacheHierarchies
```

### `cacheSource`

```python
cacheSource = cacheSource
```

### `calculatedItems`

```python
calculatedItems = calculatedItems
```

### `calculatedMembers`

```python
calculatedMembers = calculatedMembers
```

### `createdVersion`

```python
createdVersion = createdVersion
```

### `dimensions`

```python
dimensions = dimensions
```

### `enableRefresh`

```python
enableRefresh = enableRefresh
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `id`

```python
id = id
```

### `invalid`

```python
invalid = invalid
```

### `kpis`

```python
kpis = kpis
```

### `maps`

```python
maps = maps
```

### `measureGroups`

```python
measureGroups = measureGroups
```

### `mime_type`

```python
mime_type = 'application/vnd.openxmlformats-officedocument.spreadsheetml.pivotCacheDefinition+xml'
```

### `minRefreshableVersion`

```python
minRefreshableVersion = minRefreshableVersion
```

### `missingItemsLimit`

```python
missingItemsLimit = missingItemsLimit
```

### `optimizeMemory`

```python
optimizeMemory = optimizeMemory
```

### `path`

```python
path
```

### `recordCount`

```python
recordCount = recordCount
```

### `records`

```python
records = None
```

### `refreshOnLoad`

```python
refreshOnLoad = refreshOnLoad
```

### `refreshedBy`

```python
refreshedBy = refreshedBy
```

### `refreshedDate`

```python
refreshedDate = refreshedDate
```

### `refreshedDateIso`

```python
refreshedDateIso = refreshedDateIso
```

### `refreshedVersion`

```python
refreshedVersion = refreshedVersion
```

### `rel_type`

```python
rel_type = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships/pivotCacheDefinition'
```

### `saveData`

```python
saveData = saveData
```

### `supportAdvancedDrill`

```python
supportAdvancedDrill = supportAdvancedDrill
```

### `supportSubquery`

```python
supportSubquery = supportSubquery
```

### `tagname`

```python
tagname = 'pivotCacheDefinition'
```

### `to_tree`

```python
to_tree()
```

### `tupleCache`

```python
tupleCache = tupleCache
```

### `upgradeOnRefresh`

```python
upgradeOnRefresh = upgradeOnRefresh
```

## `CacheField`

```python
CacheField(sharedItems = None, fieldGroup = None, mpMap = None, extLst = None, name = None, caption = None, propertyName = None, serverField = None, uniqueList = True, numFmtId = None, formula = None, sqlType = 0, hierarchy = 0, level = 0, databaseField = True, mappingCount = None, memberPropertyField = None)
```

Bases: `Serialisable`

### `caption`

```python
caption = caption
```

### `databaseField`

```python
databaseField = databaseField
```

### `extLst`

```python
extLst = extLst
```

### `fieldGroup`

```python
fieldGroup = fieldGroup
```

### `formula`

```python
formula = formula
```

### `hierarchy`

```python
hierarchy = hierarchy
```

### `level`

```python
level = level
```

### `mappingCount`

```python
mappingCount = mappingCount
```

### `memberPropertyField`

```python
memberPropertyField = memberPropertyField
```

### `mpMap`

```python
mpMap = mpMap
```

### `name`

```python
name = name
```

### `numFmtId`

```python
numFmtId = numFmtId
```

### `propertyName`

```python
propertyName = propertyName
```

### `serverField`

```python
serverField = serverField
```

### `sharedItems`

```python
sharedItems = sharedItems
```

### `sqlType`

```python
sqlType = sqlType
```

### `tagname`

```python
tagname = 'cacheField'
```

### `uniqueList`

```python
uniqueList = uniqueList
```

## `CacheHierarchy`

```python
CacheHierarchy(uniqueName = '', caption = None, measure = None, set = None, parentSet = None, iconSet = 0, attribute = None, time = None, keyAttribute = None, defaultMemberUniqueName = None, allUniqueName = None, allCaption = None, dimensionUniqueName = None, displayFolder = None, measureGroup = None, measures = None, count = None, oneField = None, memberValueDatatype = None, unbalanced = None, unbalancedGroup = None, hidden = None, fieldsUsage = (), groupLevels = (), extLst = None)
```

Bases: `Serialisable`

### `allCaption`

```python
allCaption = allCaption
```

### `allUniqueName`

```python
allUniqueName = allUniqueName
```

### `attribute`

```python
attribute = attribute
```

### `caption`

```python
caption = caption
```

### `count`

```python
count = count
```

### `defaultMemberUniqueName`

```python
defaultMemberUniqueName = defaultMemberUniqueName
```

### `dimensionUniqueName`

```python
dimensionUniqueName = dimensionUniqueName
```

### `displayFolder`

```python
displayFolder = displayFolder
```

### `extLst`

```python
extLst = extLst
```

### `fieldsUsage`

```python
fieldsUsage = fieldsUsage
```

### `groupLevels`

```python
groupLevels = groupLevels
```

### `hidden`

```python
hidden = hidden
```

### `iconSet`

```python
iconSet = iconSet
```

### `keyAttribute`

```python
keyAttribute = keyAttribute
```

### `measure`

```python
measure = measure
```

### `measureGroup`

```python
measureGroup = measureGroup
```

### `measures`

```python
measures = measures
```

### `memberValueDatatype`

```python
memberValueDatatype = memberValueDatatype
```

### `oneField`

```python
oneField = oneField
```

### `parentSet`

```python
parentSet = parentSet
```

### `set`

```python
set = set
```

### `tagname`

```python
tagname = 'cacheHierarchy'
```

### `time`

```python
time = time
```

### `unbalanced`

```python
unbalanced = unbalanced
```

### `unbalancedGroup`

```python
unbalancedGroup = unbalancedGroup
```

### `uniqueName`

```python
uniqueName = uniqueName
```

## `CacheSource`

```python
CacheSource(type = None, connectionId = None, worksheetSource = None, consolidation = None, extLst = None)
```

Bases: `Serialisable`

### `connectionId`

```python
connectionId = connectionId
```

### `consolidation`

```python
consolidation = consolidation
```

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `tagname`

```python
tagname = 'cacheSource'
```

### `type`

```python
type = type
```

### `worksheetSource`

```python
worksheetSource = worksheetSource
```

## `CalculatedItem`

```python
CalculatedItem(field = None, formula = None, pivotArea = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = extLst
```

### `field`

```python
field = field
```

### `formula`

```python
formula = formula
```

### `pivotArea`

```python
pivotArea = pivotArea
```

### `tagname`

```python
tagname = 'calculatedItem'
```

## `CalculatedMember`

```python
CalculatedMember(name = None, mdx = None, memberName = None, hierarchy = None, parent = None, solveOrder = None, set = None, extLst = None)
```

Bases: `Serialisable`

### `extLst`

```python
extLst = Typed(expected_type=ExtensionList, allow_none=True)
```

### `hierarchy`

```python
hierarchy = hierarchy
```

### `mdx`

```python
mdx = mdx
```

### `memberName`

```python
memberName = memberName
```

### `name`

```python
name = name
```

### `parent`

```python
parent = parent
```

### `set`

```python
set = set
```

### `solveOrder`

```python
solveOrder = solveOrder
```

### `tagname`

```python
tagname = 'calculatedMember'
```

## `Consolidation`

```python
Consolidation(autoPage = None, pages = (), rangeSets = ())
```

Bases: `Serialisable`

### `autoPage`

```python
autoPage = autoPage
```

### `pages`

```python
pages = pages
```

### `rangeSets`

```python
rangeSets = rangeSets
```

### `tagname`

```python
tagname = 'consolidation'
```

## `FieldGroup`

```python
FieldGroup(par = None, base = None, rangePr = None, discretePr = (), groupItems = None)
```

Bases: `Serialisable`

### `base`

```python
base = base
```

### `discretePr`

```python
discretePr = discretePr
```

### `groupItems`

```python
groupItems = groupItems
```

### `par`

```python
par = par
```

### `rangePr`

```python
rangePr = rangePr
```

### `tagname`

```python
tagname = 'fieldGroup'
```

## `FieldUsage`

```python
FieldUsage(x = None)
```

Bases: `Serialisable`

### `tagname`

```python
tagname = 'fieldUsage'
```

### `x`

```python
x = x
```

## `GroupItems`

```python
GroupItems(count = None, m = (), n = (), b = (), e = (), s = (), d = ())
```

Bases: `Serialisable`

### `b`

```python
b = b
```

### `count`

```python
count
```

### `d`

```python
d = d
```

### `e`

```python
e = e
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

### `tagname`

```python
tagname = 'groupItems'
```

## `GroupLevel`

```python
GroupLevel(uniqueName = None, caption = None, user = None, customRollUp = None, groups = (), extLst = None)
```

Bases: `Serialisable`

### `caption`

```python
caption = caption
```

### `customRollUp`

```python
customRollUp = customRollUp
```

### `extLst`

```python
extLst = extLst
```

### `groups`

```python
groups = groups
```

### `tagname`

```python
tagname = 'groupLevel'
```

### `uniqueName`

```python
uniqueName = uniqueName
```

### `user`

```python
user = user
```

## `GroupMember`

```python
GroupMember(uniqueName = None, group = None)
```

Bases: `Serialisable`

### `group`

```python
group = group
```

### `tagname`

```python
tagname = 'groupMember'
```

### `uniqueName`

```python
uniqueName = uniqueName
```

## `LevelGroup`

```python
LevelGroup(name = None, uniqueName = None, caption = None, uniqueParent = None, id = None, groupMembers = ())
```

Bases: `Serialisable`

### `caption`

```python
caption = caption
```

### `groupMembers`

```python
groupMembers = groupMembers
```

### `id`

```python
id = id
```

### `name`

```python
name = name
```

### `tagname`

```python
tagname = 'group'
```

### `uniqueName`

```python
uniqueName = uniqueName
```

### `uniqueParent`

```python
uniqueParent = uniqueParent
```

## `MeasureDimensionMap`

```python
MeasureDimensionMap(measureGroup = None, dimension = None)
```

Bases: `Serialisable`

### `dimension`

```python
dimension = dimension
```

### `measureGroup`

```python
measureGroup = measureGroup
```

### `tagname`

```python
tagname = 'map'
```

## `MeasureGroup`

```python
MeasureGroup(name = None, caption = None)
```

Bases: `Serialisable`

### `caption`

```python
caption = caption
```

### `name`

```python
name = name
```

### `tagname`

```python
tagname = 'measureGroup'
```

## `OLAPKPI`

```python
OLAPKPI(uniqueName = None, caption = None, displayFolder = None, measureGroup = None, parent = None, value = None, goal = None, status = None, trend = None, weight = None, time = None)
```

Bases: `Serialisable`

### `caption`

```python
caption = caption
```

### `displayFolder`

```python
displayFolder = displayFolder
```

### `goal`

```python
goal = goal
```

### `measureGroup`

```python
measureGroup = measureGroup
```

### `parent`

```python
parent = parent
```

### `status`

```python
status = status
```

### `tagname`

```python
tagname = 'kpi'
```

### `time`

```python
time = time
```

### `trend`

```python
trend = trend
```

### `uniqueName`

```python
uniqueName = uniqueName
```

### `value`

```python
value = value
```

### `weight`

```python
weight = weight
```

## `OLAPSet`

```python
OLAPSet(count = None, maxRank = None, setDefinition = None, sortType = None, queryFailed = None, tpls = None, sortByTuple = None)
```

Bases: `Serialisable`

### `count`

```python
count = count
```

### `maxRank`

```python
maxRank = maxRank
```

### `queryFailed`

```python
queryFailed = queryFailed
```

### `setDefinition`

```python
setDefinition = setDefinition
```

### `sortByTuple`

```python
sortByTuple = sortByTuple
```

### `sortType`

```python
sortType = sortType
```

### `tagname`

```python
tagname = 'set'
```

### `tpls`

```python
tpls = tpls
```

## `PCDSDTCEntries`

```python
PCDSDTCEntries(count = None, m = None, n = None, e = None, s = None)
```

Bases: `Serialisable`

### `count`

```python
count = count
```

### `e`

```python
e = e
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

### `tagname`

```python
tagname = 'entries'
```

## `PageItem`

```python
PageItem(name = None)
```

Bases: `Serialisable`

### `name`

```python
name = name
```

### `tagname`

```python
tagname = 'pageItem'
```

## `PivotDimension`

```python
PivotDimension(measure = None, name = None, uniqueName = None, caption = None)
```

Bases: `Serialisable`

### `caption`

```python
caption = caption
```

### `measure`

```python
measure = measure
```

### `name`

```python
name = name
```

### `tagname`

```python
tagname = 'dimension'
```

### `uniqueName`

```python
uniqueName = uniqueName
```

## `Query`

```python
Query(mdx = None, tpls = None)
```

Bases: `Serialisable`

### `mdx`

```python
mdx = mdx
```

### `tagname`

```python
tagname = 'query'
```

### `tpls`

```python
tpls = tpls
```

## `RangePr`

```python
RangePr(autoStart = True, autoEnd = True, groupBy = 'range', startNum = None, endNum = None, startDate = None, endDate = None, groupInterval = 1)
```

Bases: `Serialisable`

### `autoEnd`

```python
autoEnd = autoEnd
```

### `autoStart`

```python
autoStart = autoStart
```

### `endDate`

```python
endDate = endDate
```

### `endNum`

```python
endNum = endNum
```

### `groupBy`

```python
groupBy = groupBy
```

### `groupInterval`

```python
groupInterval = groupInterval
```

### `startDate`

```python
startDate = startDate
```

### `startNum`

```python
startNum = startNum
```

### `tagname`

```python
tagname = 'rangePr'
```

## `RangeSet`

```python
RangeSet(i1 = None, i2 = None, i3 = None, i4 = None, ref = None, name = None, sheet = None)
```

Bases: `Serialisable`

### `i1`

```python
i1 = i1
```

### `i2`

```python
i2 = i2
```

### `i3`

```python
i3 = i3
```

### `i4`

```python
i4 = i4
```

### `name`

```python
name = name
```

### `ref`

```python
ref = ref
```

### `sheet`

```python
sheet = sheet
```

### `tagname`

```python
tagname = 'rangeSet'
```

## `ServerFormat`

```python
ServerFormat(culture = None, format = None)
```

Bases: `Serialisable`

### `culture`

```python
culture = culture
```

### `format`

```python
format = format
```

### `tagname`

```python
tagname = 'serverFormat'
```

## `SharedItems`

```python
SharedItems(_fields = (), containsSemiMixedTypes = None, containsNonDate = None, containsDate = None, containsString = None, containsBlank = None, containsMixedTypes = None, containsNumber = None, containsInteger = None, minValue = None, maxValue = None, minDate = None, maxDate = None, count = None, longText = None)
```

Bases: `Serialisable`

### `b`

```python
b = MultiSequencePart(expected_type=Boolean, store='_fields')
```

### `containsBlank`

```python
containsBlank = containsBlank
```

### `containsDate`

```python
containsDate = containsDate
```

### `containsInteger`

```python
containsInteger = containsInteger
```

### `containsMixedTypes`

```python
containsMixedTypes = containsMixedTypes
```

### `containsNonDate`

```python
containsNonDate = containsNonDate
```

### `containsNumber`

```python
containsNumber = containsNumber
```

### `containsSemiMixedTypes`

```python
containsSemiMixedTypes = containsSemiMixedTypes
```

### `containsString`

```python
containsString = containsString
```

### `count`

```python
count
```

### `d`

```python
d = MultiSequencePart(expected_type=DateTimeField, store='_fields')
```

### `e`

```python
e = MultiSequencePart(expected_type=Error, store='_fields')
```

### `longText`

```python
longText = longText
```

### `m`

```python
m = MultiSequencePart(expected_type=Missing, store='_fields')
```

### `maxDate`

```python
maxDate = maxDate
```

### `maxValue`

```python
maxValue = maxValue
```

### `minDate`

```python
minDate = minDate
```

### `minValue`

```python
minValue = minValue
```

### `n`

```python
n = MultiSequencePart(expected_type=Number, store='_fields')
```

### `s`

```python
s = MultiSequencePart(expected_type=Text, store='_fields')
```

### `tagname`

```python
tagname = 'sharedItems'
```

## `TupleCache`

```python
TupleCache(entries = None, sets = (), queryCache = (), serverFormats = (), extLst = None)
```

Bases: `Serialisable`

### `entries`

```python
entries = entries
```

### `extLst`

```python
extLst = extLst
```

### `queryCache`

```python
queryCache = queryCache
```

### `serverFormats`

```python
serverFormats = serverFormats
```

### `sets`

```python
sets = sets
```

### `tagname`

```python
tagname = 'tupleCache'
```

## `WorksheetSource`

```python
WorksheetSource(ref = None, name = None, sheet = None)
```

Bases: `Serialisable`

### `name`

```python
name = name
```

### `ref`

```python
ref = ref
```

### `sheet`

```python
sheet = sheet
```

### `tagname`

```python
tagname = 'worksheetSource'
```
