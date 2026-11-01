<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.xml.constants`

Constants for fixed paths in a file and xml namespace urls.

## `ACTIVEX`

```python
ACTIVEX = 'application/vnd.ms-office.activeX+xml'
```

## `ARC_APP`

```python
ARC_APP = PACKAGE_PROPS + '/app.xml'
```

## `ARC_CONTENT_TYPES`

```python
ARC_CONTENT_TYPES = '[Content_Types].xml'
```

## `ARC_CORE`

```python
ARC_CORE = PACKAGE_PROPS + '/core.xml'
```

## `ARC_CUSTOM`

```python
ARC_CUSTOM = PACKAGE_PROPS + '/custom.xml'
```

## `ARC_CUSTOM_UI`

```python
ARC_CUSTOM_UI = 'customUI/customUI.xml'
```

## `ARC_ROOT_RELS`

```python
ARC_ROOT_RELS = PACKAGE_RELS + '/.rels'
```

## `ARC_SHARED_STRINGS`

```python
ARC_SHARED_STRINGS = PACKAGE_XL + '/sharedStrings.xml'
```

## `ARC_STYLE`

```python
ARC_STYLE = PACKAGE_XL + '/styles.xml'
```

## `ARC_THEME`

```python
ARC_THEME = PACKAGE_THEME + '/theme1.xml'
```

## `ARC_WORKBOOK`

```python
ARC_WORKBOOK = PACKAGE_XL + '/workbook.xml'
```

## `ARC_WORKBOOK_RELS`

```python
ARC_WORKBOOK_RELS = PACKAGE_XL + '/' + PACKAGE_RELS + '/workbook.xml.rels'
```

## `CHARTSHAPE_TYPE`

```python
CHARTSHAPE_TYPE = 'application/vnd.openxmlformats-officedocument.drawingml.chartshapes+xml'
```

## `CHARTSHEET_TYPE`

```python
CHARTSHEET_TYPE = SPREADSHEET % 'chartsheet'
```

## `CHART_DRAWING_NS`

```python
CHART_DRAWING_NS = 'http://schemas.openxmlformats.org/drawingml/2006/chartDrawing'
```

## `CHART_NS`

```python
CHART_NS = 'http://schemas.openxmlformats.org/drawingml/2006/chart'
```

## `CHART_TYPE`

```python
CHART_TYPE = 'application/vnd.openxmlformats-officedocument.drawingml.chart+xml'
```

## `COMMENTS_NS`

```python
COMMENTS_NS = REL_NS + '/comments'
```

## `COMMENTS_TYPE`

```python
COMMENTS_TYPE = SPREADSHEET % 'comments'
```

## `CONTYPES_NS`

```python
CONTYPES_NS = PKG_NS + 'content-types'
```

## `COREPROPS_NS`

```python
COREPROPS_NS = PKG_NS + 'metadata/core-properties'
```

## `CPROPS_FMTID`

```python
CPROPS_FMTID = '{D5CDD505-2E9C-101B-9397-08002B2CF9AE}'
```

## `CPROPS_TYPE`

```python
CPROPS_TYPE = 'application/vnd.openxmlformats-officedocument.custom-properties+xml'
```

## `CTRL`

```python
CTRL = 'application/vnd.ms-excel.controlproperties+xml'
```

## `CUSTOMUI_NS`

```python
CUSTOMUI_NS = 'http://schemas.microsoft.com/office/2006/relationships/ui/extensibility'
```

## `CUSTPROPS_NS`

```python
CUSTPROPS_NS = DOC_NS + 'custom-properties'
```

## `DCORE_NS`

```python
DCORE_NS = 'http://purl.org/dc/elements/1.1/'
```

## `DCTERMS_NS`

```python
DCTERMS_NS = 'http://purl.org/dc/terms/'
```

## `DCTERMS_PREFIX`

```python
DCTERMS_PREFIX = 'dcterms'
```

## `DOC_NS`

```python
DOC_NS = 'http://schemas.openxmlformats.org/officeDocument/2006/'
```

## `DRAWING_NS`

```python
DRAWING_NS = 'http://schemas.openxmlformats.org/drawingml/2006/main'
```

## `DRAWING_TYPE`

```python
DRAWING_TYPE = 'application/vnd.openxmlformats-officedocument.drawing+xml'
```

## `EXTERNAL_LINK`

```python
EXTERNAL_LINK = SPREADSHEET % 'externalLink'
```

## `EXTERNAL_LINK_NS`

```python
EXTERNAL_LINK_NS = REL_NS + '/externalLink'
```

## `EXT_TYPES`

```python
EXT_TYPES = {'{78C0D931-6437-407D-A8EE-F0AAD7539E65}': 'Conditional Formatting', '{CCE6A557-97BC-4B89-ADB6-D9C93CAAB3DF}': 'Data Validation', '{05C60535-1F16-4FD2-B633-F4F36F0B64E0}': 'Sparkline Group', '{A8765BA9-456A-4DAB-B4F3-ACF838C121DE}': 'Slicer List', '{FC87AEE6-9EDD-4A0A-B7FB-166176984837}': 'Protected Range', '{01252117-D84E-4E92-8308-4BE1C098FCBB}': 'Ignored Error', '{F7C9EE02-42E1-4005-9D12-6889AFFD525C}': 'Web Extension', '{3A4CF648-6AED-40f4-86FF-DC5316D8AED3}': 'Slicer List', '{7E03D99C-DC04-49d9-9315-930204A7B6E9}': 'Timeline Ref'}
```

## `IMAGE_NS`

```python
IMAGE_NS = REL_NS + '/image'
```

## `MAX_COLUMN`

```python
MAX_COLUMN = 16384
```

## `MAX_ROW`

```python
MAX_ROW = 1048576
```

## `MIN_COLUMN`

```python
MIN_COLUMN = 0
```

## `MIN_ROW`

```python
MIN_ROW = 0
```

## `NAMESPACES`

```python
NAMESPACES = {'cp': COREPROPS_NS, 'dc': DCORE_NS, DCTERMS_PREFIX: DCTERMS_NS, 'dcmitype': 'http://purl.org/dc/dcmitype/', 'xsi': XSI_NS, 'vt': VTYPES_NS, 'xml': XML_NS, 'main': SHEET_MAIN_NS, 'cust': CUSTPROPS_NS}
```

## `PACKAGE_CHARTS`

```python
PACKAGE_CHARTS = PACKAGE_XL + '/' + 'charts'
```

## `PACKAGE_CHARTSHEETS`

```python
PACKAGE_CHARTSHEETS = PACKAGE_XL + '/' + 'chartsheets'
```

## `PACKAGE_CHARTSHEETS_RELS`

```python
PACKAGE_CHARTSHEETS_RELS = PACKAGE_CHARTSHEETS + '/' + '_rels'
```

## `PACKAGE_DRAWINGS`

```python
PACKAGE_DRAWINGS = PACKAGE_XL + '/' + 'drawings'
```

## `PACKAGE_IMAGES`

```python
PACKAGE_IMAGES = PACKAGE_XL + '/' + 'media'
```

## `PACKAGE_PIVOT_CACHE`

```python
PACKAGE_PIVOT_CACHE = PACKAGE_XL + '/' + 'pivotCache'
```

## `PACKAGE_PIVOT_TABLE`

```python
PACKAGE_PIVOT_TABLE = PACKAGE_XL + '/' + 'pivotTables'
```

## `PACKAGE_PROPS`

```python
PACKAGE_PROPS = 'docProps'
```

## `PACKAGE_RELS`

```python
PACKAGE_RELS = '_rels'
```

## `PACKAGE_THEME`

```python
PACKAGE_THEME = PACKAGE_XL + '/' + 'theme'
```

## `PACKAGE_WORKSHEETS`

```python
PACKAGE_WORKSHEETS = PACKAGE_XL + '/' + 'worksheets'
```

## `PACKAGE_WORKSHEET_RELS`

```python
PACKAGE_WORKSHEET_RELS = PACKAGE_WORKSHEETS + '/' + '_rels'
```

## `PACKAGE_XL`

```python
PACKAGE_XL = 'xl'
```

## `PKG_NS`

```python
PKG_NS = 'http://schemas.openxmlformats.org/package/2006/'
```

## `PKG_REL_NS`

```python
PKG_REL_NS = PKG_NS + 'relationships'
```

## `REL_NS`

```python
REL_NS = DOC_NS + 'relationships'
```

## `SHARED_STRINGS`

```python
SHARED_STRINGS = SPREADSHEET % 'sharedStrings'
```

## `SHEET_DRAWING_NS`

```python
SHEET_DRAWING_NS = 'http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing'
```

## `SHEET_MAIN_NS`

```python
SHEET_MAIN_NS = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main'
```

## `SPREADSHEET`

```python
SPREADSHEET = 'application/vnd.openxmlformats-officedocument.spreadsheetml.%s+xml'
```

## `STYLES_TYPE`

```python
STYLES_TYPE = SPREADSHEET % 'styles'
```

## `THEME_TYPE`

```python
THEME_TYPE = 'application/vnd.openxmlformats-officedocument.theme+xml'
```

## `VBA`

```python
VBA = 'application/vnd.ms-office.vbaProject'
```

## `VML_NS`

```python
VML_NS = REL_NS + '/vmlDrawing'
```

## `VTYPES_NS`

```python
VTYPES_NS = DOC_NS + 'docPropsVTypes'
```

## `WORKBOOK`

```python
WORKBOOK = 'application/vnd.openxmlformats-officedocument.spreadsheetml.%s.main+xml'
```

## `WORKBOOK_MACRO`

```python
WORKBOOK_MACRO = 'application/vnd.ms-excel.%s.macroEnabled.main+xml'
```

## `WORKSHEET_TYPE`

```python
WORKSHEET_TYPE = SPREADSHEET % 'worksheet'
```

## `XLSM`

```python
XLSM = WORKBOOK_MACRO % 'sheet'
```

## `XLSX`

```python
XLSX = WORKBOOK % 'sheet'
```

## `XLTM`

```python
XLTM = WORKBOOK_MACRO % 'template'
```

## `XLTX`

```python
XLTX = WORKBOOK % 'template'
```

## `XML_NS`

```python
XML_NS = 'http://www.w3.org/XML/1998/namespace'
```

## `XPROPS_NS`

```python
XPROPS_NS = DOC_NS + 'extended-properties'
```

## `XSI_NS`

```python
XSI_NS = 'http://www.w3.org/2001/XMLSchema-instance'
```
