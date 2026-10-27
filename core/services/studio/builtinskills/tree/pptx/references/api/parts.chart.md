<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.parts.chart`

Chart part objects, including Chart and Charts.

## `ChartPart`

Bases: `XmlPart`

A chart part.

Corresponds to parts having partnames matching ppt/charts/chart[1-9][0-9]*.xml

### `chart`

```python
chart()
```

`Chart` object representing the chart in this part.

### `chart_workbook`

```python
chart_workbook()
```

The `ChartWorkbook` object providing access to the external chart
data in a linked or embedded Excel workbook.

### `new`

```python
new(chart_type: XL_CHART_TYPE, chart_data: ChartData, package: Package)
```

Return new `ChartPart` instance added to `package`.

Returned chart-part contains a chart of `chart_type` depicting `chart_data`.

### `partname_template`

```python
partname_template = '/ppt/charts/chart%d.xml'
```

## `ChartWorkbook`

```python
ChartWorkbook(chartSpace, chart_part)
```

Bases: `object`

Provides access to external chart data in a linked or embedded Excel workbook.

### `update_from_xlsx_blob`

```python
update_from_xlsx_blob(xlsx_blob)
```

Replace the Excel spreadsheet in the related `EmbeddedXlsxPart` with
the Excel binary in *xlsx_blob*, adding a new `EmbeddedXlsxPart` if
there isn't one.

### `xlsx_part`

```python
xlsx_part
```

Optional `EmbeddedXlsxPart` object containing data for this chart.

This related part has its rId at `c:chartSpace/c:externalData/@rId`. This value
is `None` if there is no `<c:externalData>` element.
