<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.xlsx`

Chart builder and related objects.

## `BubbleWorkbookWriter`

Bases: `XyWorkbookWriter`

Service object that knows how to write an Excel workbook from bubble
chart data.

### `bubble_sizes_ref`

```python
bubble_sizes_ref(series)
```

The Excel worksheet reference to the range containing the bubble
sizes for *series* (not including the column heading cell).

## `CategoryWorkbookWriter`

Bases: `_BaseWorkbookWriter`

Determines Excel worksheet layout and can write an Excel workbook from
a CategoryChartData object. Serves as the authority for Excel worksheet
ranges.

### `categories_ref`

```python
categories_ref
```

The Excel worksheet reference to the categories for this chart (not
including the column heading).

### `series_name_ref`

```python
series_name_ref(series)
```

Return the Excel worksheet reference to the cell containing the name
for *series*. This also serves as the column heading for the series
values.

### `values_ref`

```python
values_ref(series)
```

The Excel worksheet reference to the values for this series (not
including the column heading).

## `XyWorkbookWriter`

Bases: `_BaseWorkbookWriter`

Determines Excel worksheet layout and can write an Excel workbook from XY
chart data. Serves as the authority for Excel worksheet ranges.

### `series_name_ref`

```python
series_name_ref(series)
```

Return the Excel worksheet reference to the cell containing the name
for *series*. This also serves as the column heading for the series
Y values.

### `series_table_row_offset`

```python
series_table_row_offset(series)
```

Return the number of rows preceding the data table for *series* in
the Excel worksheet.

### `x_values_ref`

```python
x_values_ref(series)
```

The Excel worksheet reference to the X values for this chart (not
including the column label).

### `y_values_ref`

```python
y_values_ref(series)
```

The Excel worksheet reference to the Y values for this chart (not
including the column label).
