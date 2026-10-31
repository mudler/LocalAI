<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.chart.plot`

Plot-related objects.

## `Area3DPlot`

Bases: `_BasePlot`

A 3-dimensional area plot.

## `AreaPlot`

Bases: `_BasePlot`

An area plot.

## `BarPlot`

Bases: `_BasePlot`

A bar chart-style plot.

### `gap_width`

```python
gap_width
```

Width of gap between bar(s) of each category, as an integer
percentage of the bar width. The default value for a new bar chart is
150, representing 150% or 1.5 times the width of a single bar.

### `overlap`

```python
overlap
```

Read/write int value in range -100..100 specifying a percentage of
the bar width by which to overlap adjacent bars in a multi-series bar
chart. Default is 0. A setting of -100 creates a gap of a full bar
width and a setting of 100 causes all the bars in a category to be
superimposed. A stacked bar plot has overlap of 100 by default.

## `BubblePlot`

Bases: `_BasePlot`

A bubble chart plot.

### `bubble_scale`

```python
bubble_scale
```

An integer between 0 and 300 inclusive indicating the percentage of
the default size at which bubbles should be displayed. Assigning
`None` produces the same behavior as assigning `100`.

## `DoughnutPlot`

Bases: `_BasePlot`

An doughnut plot.

## `LinePlot`

Bases: `_BasePlot`

A line chart-style plot.

## `PiePlot`

Bases: `_BasePlot`

A pie chart-style plot.

## `PlotFactory`

```python
PlotFactory(xChart, chart)
```

Return an instance of the appropriate subclass of _BasePlot based on the
tagname of *xChart*.

## `PlotTypeInspector`

Bases: `object`

"One-shot" service object that knows how to identify the type of a plot
as a member of the XL_CHART_TYPE enumeration.

### `chart_type`

```python
chart_type(plot)
```

Return the member of `XlChartType` that corresponds to the chart
type of *plot*.

## `RadarPlot`

Bases: `_BasePlot`

A radar-style plot.

## `XyPlot`

Bases: `_BasePlot`

An XY (scatter) plot.
