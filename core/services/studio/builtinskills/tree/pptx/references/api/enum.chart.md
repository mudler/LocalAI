<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.enum.chart`

Enumerations used by charts and related objects.

## `XL_AXIS_CROSSES`

Bases: `BaseXmlEnum`

Specifies the point on an axis where the other axis crosses.

Example::

    from pptx.enum.chart import XL_AXIS_CROSSES

    value_axis.crosses = XL_AXIS_CROSSES.MAXIMUM

MS API Name: `XlAxisCrosses`

https://msdn.microsoft.com/en-us/library/office/ff745402.aspx

### `AUTOMATIC`

```python
AUTOMATIC = (-4105, 'autoZero', 'The axis crossing point is set automatically, often at zero.')
```

The axis crossing point is set automatically, often at zero.

### `CUSTOM`

```python
CUSTOM = (-4114, '', 'The .crosses_at property specifies the axis crossing point.')
```

The .crosses_at property specifies the axis crossing point.

### `MAXIMUM`

```python
MAXIMUM = (2, 'max', 'The axis crosses at the maximum value.')
```

The axis crosses at the maximum value.

### `MINIMUM`

```python
MINIMUM = (4, 'min', 'The axis crosses at the minimum value.')
```

The axis crosses at the minimum value.

## `XL_CATEGORY_TYPE`

Bases: `BaseEnum`

Specifies the type of the category axis.

Example::

    from pptx.enum.chart import XL_CATEGORY_TYPE

    date_axis = chart.category_axis
    assert date_axis.category_type == XL_CATEGORY_TYPE.TIME_SCALE

MS API Name: `XlCategoryType`

https://msdn.microsoft.com/EN-US/library/office/ff746136.aspx

### `AUTOMATIC_SCALE`

```python
AUTOMATIC_SCALE = (-4105, 'The application controls the axis type.')
```

The application controls the axis type.

### `CATEGORY_SCALE`

```python
CATEGORY_SCALE = (2, 'Axis groups data by an arbitrary set of categories')
```

Axis groups data by an arbitrary set of categories

### `TIME_SCALE`

```python
TIME_SCALE = (3, 'Axis groups data on a time scale of days, months, or years.')
```

Axis groups data on a time scale of days, months, or years.

## `XL_CHART_TYPE`

Bases: `BaseEnum`

Specifies the type of a chart.

Example::

    from pptx.enum.chart import XL_CHART_TYPE

    assert chart.chart_type == XL_CHART_TYPE.BAR_STACKED

MS API Name: `XlChartType`

http://msdn.microsoft.com/en-us/library/office/ff838409.aspx

### `AREA`

```python
AREA = (1, 'Area')
```

Area

### `AREA_STACKED`

```python
AREA_STACKED = (76, 'Stacked Area.')
```

Stacked Area.

### `AREA_STACKED_100`

```python
AREA_STACKED_100 = (77, '100% Stacked Area.')
```

100% Stacked Area.

### `BAR_CLUSTERED`

```python
BAR_CLUSTERED = (57, 'Clustered Bar.')
```

Clustered Bar.

### `BAR_OF_PIE`

```python
BAR_OF_PIE = (71, 'Bar of Pie.')
```

Bar of Pie.

### `BAR_STACKED`

```python
BAR_STACKED = (58, 'Stacked Bar.')
```

Stacked Bar.

### `BAR_STACKED_100`

```python
BAR_STACKED_100 = (59, '100% Stacked Bar.')
```

100% Stacked Bar.

### `BUBBLE`

```python
BUBBLE = (15, 'Bubble.')
```

Bubble.

### `BUBBLE_THREE_D_EFFECT`

```python
BUBBLE_THREE_D_EFFECT = (87, 'Bubble with 3D effects.')
```

Bubble with 3D effects.

### `COLUMN_CLUSTERED`

```python
COLUMN_CLUSTERED = (51, 'Clustered Column.')
```

Clustered Column.

### `COLUMN_STACKED`

```python
COLUMN_STACKED = (52, 'Stacked Column.')
```

Stacked Column.

### `COLUMN_STACKED_100`

```python
COLUMN_STACKED_100 = (53, '100% Stacked Column.')
```

100% Stacked Column.

### `CONE_BAR_CLUSTERED`

```python
CONE_BAR_CLUSTERED = (102, 'Clustered Cone Bar.')
```

Clustered Cone Bar.

### `CONE_BAR_STACKED`

```python
CONE_BAR_STACKED = (103, 'Stacked Cone Bar.')
```

Stacked Cone Bar.

### `CONE_BAR_STACKED_100`

```python
CONE_BAR_STACKED_100 = (104, '100% Stacked Cone Bar.')
```

100% Stacked Cone Bar.

### `CONE_COL`

```python
CONE_COL = (105, '3D Cone Column.')
```

3D Cone Column.

### `CONE_COL_CLUSTERED`

```python
CONE_COL_CLUSTERED = (99, 'Clustered Cone Column.')
```

Clustered Cone Column.

### `CONE_COL_STACKED`

```python
CONE_COL_STACKED = (100, 'Stacked Cone Column.')
```

Stacked Cone Column.

### `CONE_COL_STACKED_100`

```python
CONE_COL_STACKED_100 = (101, '100% Stacked Cone Column.')
```

100% Stacked Cone Column.

### `CYLINDER_BAR_CLUSTERED`

```python
CYLINDER_BAR_CLUSTERED = (95, 'Clustered Cylinder Bar.')
```

Clustered Cylinder Bar.

### `CYLINDER_BAR_STACKED`

```python
CYLINDER_BAR_STACKED = (96, 'Stacked Cylinder Bar.')
```

Stacked Cylinder Bar.

### `CYLINDER_BAR_STACKED_100`

```python
CYLINDER_BAR_STACKED_100 = (97, '100% Stacked Cylinder Bar.')
```

100% Stacked Cylinder Bar.

### `CYLINDER_COL`

```python
CYLINDER_COL = (98, '3D Cylinder Column.')
```

3D Cylinder Column.

### `CYLINDER_COL_CLUSTERED`

```python
CYLINDER_COL_CLUSTERED = (92, 'Clustered Cone Column.')
```

Clustered Cone Column.

### `CYLINDER_COL_STACKED`

```python
CYLINDER_COL_STACKED = (93, 'Stacked Cone Column.')
```

Stacked Cone Column.

### `CYLINDER_COL_STACKED_100`

```python
CYLINDER_COL_STACKED_100 = (94, '100% Stacked Cylinder Column.')
```

100% Stacked Cylinder Column.

### `DOUGHNUT`

```python
DOUGHNUT = (-4120, 'Doughnut.')
```

Doughnut.

### `DOUGHNUT_EXPLODED`

```python
DOUGHNUT_EXPLODED = (80, 'Exploded Doughnut.')
```

Exploded Doughnut.

### `LINE`

```python
LINE = (4, 'Line.')
```

Line.

### `LINE_MARKERS`

```python
LINE_MARKERS = (65, 'Line with Markers.')
```

Line with Markers.

### `LINE_MARKERS_STACKED`

```python
LINE_MARKERS_STACKED = (66, 'Stacked Line with Markers.')
```

Stacked Line with Markers.

### `LINE_MARKERS_STACKED_100`

```python
LINE_MARKERS_STACKED_100 = (67, '100% Stacked Line with Markers.')
```

100% Stacked Line with Markers.

### `LINE_STACKED`

```python
LINE_STACKED = (63, 'Stacked Line.')
```

Stacked Line.

### `LINE_STACKED_100`

```python
LINE_STACKED_100 = (64, '100% Stacked Line.')
```

100% Stacked Line.

### `PIE`

```python
PIE = (5, 'Pie.')
```

Pie.

### `PIE_EXPLODED`

```python
PIE_EXPLODED = (69, 'Exploded Pie.')
```

Exploded Pie.

### `PIE_OF_PIE`

```python
PIE_OF_PIE = (68, 'Pie of Pie.')
```

Pie of Pie.

### `PYRAMID_BAR_CLUSTERED`

```python
PYRAMID_BAR_CLUSTERED = (109, 'Clustered Pyramid Bar.')
```

Clustered Pyramid Bar.

### `PYRAMID_BAR_STACKED`

```python
PYRAMID_BAR_STACKED = (110, 'Stacked Pyramid Bar.')
```

Stacked Pyramid Bar.

### `PYRAMID_BAR_STACKED_100`

```python
PYRAMID_BAR_STACKED_100 = (111, '100% Stacked Pyramid Bar.')
```

100% Stacked Pyramid Bar.

### `PYRAMID_COL`

```python
PYRAMID_COL = (112, '3D Pyramid Column.')
```

3D Pyramid Column.

### `PYRAMID_COL_CLUSTERED`

```python
PYRAMID_COL_CLUSTERED = (106, 'Clustered Pyramid Column.')
```

Clustered Pyramid Column.

### `PYRAMID_COL_STACKED`

```python
PYRAMID_COL_STACKED = (107, 'Stacked Pyramid Column.')
```

Stacked Pyramid Column.

### `PYRAMID_COL_STACKED_100`

```python
PYRAMID_COL_STACKED_100 = (108, '100% Stacked Pyramid Column.')
```

100% Stacked Pyramid Column.

### `RADAR`

```python
RADAR = (-4151, 'Radar.')
```

Radar.

### `RADAR_FILLED`

```python
RADAR_FILLED = (82, 'Filled Radar.')
```

Filled Radar.

### `RADAR_MARKERS`

```python
RADAR_MARKERS = (81, 'Radar with Data Markers.')
```

Radar with Data Markers.

### `STOCK_HLC`

```python
STOCK_HLC = (88, 'High-Low-Close.')
```

High-Low-Close.

### `STOCK_OHLC`

```python
STOCK_OHLC = (89, 'Open-High-Low-Close.')
```

Open-High-Low-Close.

### `STOCK_VHLC`

```python
STOCK_VHLC = (90, 'Volume-High-Low-Close.')
```

Volume-High-Low-Close.

### `STOCK_VOHLC`

```python
STOCK_VOHLC = (91, 'Volume-Open-High-Low-Close.')
```

Volume-Open-High-Low-Close.

### `SURFACE`

```python
SURFACE = (83, '3D Surface.')
```

3D Surface.

### `SURFACE_TOP_VIEW`

```python
SURFACE_TOP_VIEW = (85, 'Surface (Top View).')
```

Surface (Top View).

### `SURFACE_TOP_VIEW_WIREFRAME`

```python
SURFACE_TOP_VIEW_WIREFRAME = (86, 'Surface (Top View wireframe).')
```

Surface (Top View wireframe).

### `SURFACE_WIREFRAME`

```python
SURFACE_WIREFRAME = (84, '3D Surface (wireframe).')
```

3D Surface (wireframe).

### `THREE_D_AREA`

```python
THREE_D_AREA = (-4098, '3D Area.')
```

3D Area.

### `THREE_D_AREA_STACKED`

```python
THREE_D_AREA_STACKED = (78, '3D Stacked Area.')
```

3D Stacked Area.

### `THREE_D_AREA_STACKED_100`

```python
THREE_D_AREA_STACKED_100 = (79, '100% Stacked Area.')
```

100% Stacked Area.

### `THREE_D_BAR_CLUSTERED`

```python
THREE_D_BAR_CLUSTERED = (60, '3D Clustered Bar.')
```

3D Clustered Bar.

### `THREE_D_BAR_STACKED`

```python
THREE_D_BAR_STACKED = (61, '3D Stacked Bar.')
```

3D Stacked Bar.

### `THREE_D_BAR_STACKED_100`

```python
THREE_D_BAR_STACKED_100 = (62, '3D 100% Stacked Bar.')
```

3D 100% Stacked Bar.

### `THREE_D_COLUMN`

```python
THREE_D_COLUMN = (-4100, '3D Column.')
```

3D Column.

### `THREE_D_COLUMN_CLUSTERED`

```python
THREE_D_COLUMN_CLUSTERED = (54, '3D Clustered Column.')
```

3D Clustered Column.

### `THREE_D_COLUMN_STACKED`

```python
THREE_D_COLUMN_STACKED = (55, '3D Stacked Column.')
```

3D Stacked Column.

### `THREE_D_COLUMN_STACKED_100`

```python
THREE_D_COLUMN_STACKED_100 = (56, '3D 100% Stacked Column.')
```

3D 100% Stacked Column.

### `THREE_D_LINE`

```python
THREE_D_LINE = (-4101, '3D Line.')
```

3D Line.

### `THREE_D_PIE`

```python
THREE_D_PIE = (-4102, '3D Pie.')
```

3D Pie.

### `THREE_D_PIE_EXPLODED`

```python
THREE_D_PIE_EXPLODED = (70, 'Exploded 3D Pie.')
```

Exploded 3D Pie.

### `XY_SCATTER`

```python
XY_SCATTER = (-4169, 'Scatter.')
```

Scatter.

### `XY_SCATTER_LINES`

```python
XY_SCATTER_LINES = (74, 'Scatter with Lines.')
```

Scatter with Lines.

### `XY_SCATTER_LINES_NO_MARKERS`

```python
XY_SCATTER_LINES_NO_MARKERS = (75, 'Scatter with Lines and No Data Markers.')
```

Scatter with Lines and No Data Markers.

### `XY_SCATTER_SMOOTH`

```python
XY_SCATTER_SMOOTH = (72, 'Scatter with Smoothed Lines.')
```

Scatter with Smoothed Lines.

### `XY_SCATTER_SMOOTH_NO_MARKERS`

```python
XY_SCATTER_SMOOTH_NO_MARKERS = (73, 'Scatter with Smoothed Lines and No Data Markers.')
```

Scatter with Smoothed Lines and No Data Markers.

## `XL_DATA_LABEL_POSITION`

Bases: `BaseXmlEnum`

Specifies where the data label is positioned.

Example::

    from pptx.enum.chart import XL_LABEL_POSITION

    data_labels = chart.plots[0].data_labels
    data_labels.position = XL_LABEL_POSITION.OUTSIDE_END

MS API Name: `XlDataLabelPosition`

http://msdn.microsoft.com/en-us/library/office/ff745082.aspx

### `ABOVE`

```python
ABOVE = (0, 't', 'The data label is positioned above the data point.')
```

The data label is positioned above the data point.

### `BELOW`

```python
BELOW = (1, 'b', 'The data label is positioned below the data point.')
```

The data label is positioned below the data point.

### `BEST_FIT`

```python
BEST_FIT = (5, 'bestFit', 'Word sets the position of the data label.')
```

Word sets the position of the data label.

### `CENTER`

```python
CENTER = (-4108, 'ctr', 'The data label is centered on the data point or inside a bar or a pie slice.')
```

The data label is centered on the data point or inside a bar or a pie slice.

### `INSIDE_BASE`

```python
INSIDE_BASE = (4, 'inBase', 'The data label is positioned inside the data point at the bottom edge.')
```

The data label is positioned inside the data point at the bottom edge.

### `INSIDE_END`

```python
INSIDE_END = (3, 'inEnd', 'The data label is positioned inside the data point at the top edge.')
```

The data label is positioned inside the data point at the top edge.

### `LEFT`

```python
LEFT = (-4131, 'l', 'The data label is positioned to the left of the data point.')
```

The data label is positioned to the left of the data point.

### `MIXED`

```python
MIXED = (6, '', 'Data labels are in multiple positions (read-only).')
```

Data labels are in multiple positions (read-only).

### `OUTSIDE_END`

```python
OUTSIDE_END = (2, 'outEnd', 'The data label is positioned outside the data point at the top edge.')
```

The data label is positioned outside the data point at the top edge.

### `RIGHT`

```python
RIGHT = (-4152, 'r', 'The data label is positioned to the right of the data point.')
```

The data label is positioned to the right of the data point.

## `XL_LABEL_POSITION`

```python
XL_LABEL_POSITION = XL_DATA_LABEL_POSITION
```

## `XL_LEGEND_POSITION`

Bases: `BaseXmlEnum`

Specifies the position of the legend on a chart.

Example::

    from pptx.enum.chart import XL_LEGEND_POSITION

    chart.has_legend = True
    chart.legend.position = XL_LEGEND_POSITION.BOTTOM

MS API Name: `XlLegendPosition`

http://msdn.microsoft.com/en-us/library/office/ff745840.aspx

### `BOTTOM`

```python
BOTTOM = (-4107, 'b', 'Below the chart.')
```

Below the chart.

### `CORNER`

```python
CORNER = (2, 'tr', 'In the upper-right corner of the chart border.')
```

In the upper-right corner of the chart border.

### `CUSTOM`

```python
CUSTOM = (-4161, '', 'A custom position (read-only).')
```

A custom position (read-only).

### `LEFT`

```python
LEFT = (-4131, 'l', 'Left of the chart.')
```

Left of the chart.

### `RIGHT`

```python
RIGHT = (-4152, 'r', 'Right of the chart.')
```

Right of the chart.

### `TOP`

```python
TOP = (-4160, 't', 'Above the chart.')
```

Above the chart.

## `XL_MARKER_STYLE`

Bases: `BaseXmlEnum`

Specifies the marker style for a point or series in a line, scatter, or radar chart.

Example::

    from pptx.enum.chart import XL_MARKER_STYLE

    series.marker.style = XL_MARKER_STYLE.CIRCLE

MS API Name: `XlMarkerStyle`

http://msdn.microsoft.com/en-us/library/office/ff197219.aspx

### `AUTOMATIC`

```python
AUTOMATIC = (-4105, 'auto', 'Automatic markers')
```

Automatic markers

### `CIRCLE`

```python
CIRCLE = (8, 'circle', 'Circular markers')
```

Circular markers

### `DASH`

```python
DASH = (-4115, 'dash', 'Long bar markers')
```

Long bar markers

### `DIAMOND`

```python
DIAMOND = (2, 'diamond', 'Diamond-shaped markers')
```

Diamond-shaped markers

### `DOT`

```python
DOT = (-4118, 'dot', 'Short bar markers')
```

Short bar markers

### `NONE`

```python
NONE = (-4142, 'none', 'No markers')
```

No markers

### `PICTURE`

```python
PICTURE = (-4147, 'picture', 'Picture markers')
```

Picture markers

### `PLUS`

```python
PLUS = (9, 'plus', 'Square markers with a plus sign')
```

Square markers with a plus sign

### `SQUARE`

```python
SQUARE = (1, 'square', 'Square markers')
```

Square markers

### `STAR`

```python
STAR = (5, 'star', 'Square markers with an  asterisk')
```

Square markers with an  asterisk

### `TRIANGLE`

```python
TRIANGLE = (3, 'triangle', 'Triangular markers')
```

Triangular markers

### `X`

```python
X = (-4168, 'x', 'Square markers with an X')
```

Square markers with an X

## `XL_TICK_LABEL_POSITION`

Bases: `BaseXmlEnum`

Specifies the position of tick-mark labels on a chart axis.

Example::

    from pptx.enum.chart import XL_TICK_LABEL_POSITION

    category_axis = chart.category_axis
    category_axis.tick_label_position = XL_TICK_LABEL_POSITION.LOW

MS API Name: `XlTickLabelPosition`

http://msdn.microsoft.com/en-us/library/office/ff822561.aspx

### `HIGH`

```python
HIGH = (-4127, 'high', 'Top or right side of the chart.')
```

Top or right side of the chart.

### `LOW`

```python
LOW = (-4134, 'low', 'Bottom or left side of the chart.')
```

Bottom or left side of the chart.

### `NEXT_TO_AXIS`

```python
NEXT_TO_AXIS = (4, 'nextTo', 'Next to axis (where axis is not at either side of the chart).')
```

Next to axis (where axis is not at either side of the chart).

### `NONE`

```python
NONE = (-4142, 'none', 'No tick labels.')
```

No tick labels.

## `XL_TICK_MARK`

Bases: `BaseXmlEnum`

Specifies a type of axis tick for a chart.

Example::

    from pptx.enum.chart import XL_TICK_MARK

    chart.value_axis.minor_tick_mark = XL_TICK_MARK.INSIDE

MS API Name: `XlTickMark`

http://msdn.microsoft.com/en-us/library/office/ff193878.aspx

### `CROSS`

```python
CROSS = (4, 'cross', 'Tick mark crosses the axis')
```

Tick mark crosses the axis

### `INSIDE`

```python
INSIDE = (2, 'in', 'Tick mark appears inside the axis')
```

Tick mark appears inside the axis

### `NONE`

```python
NONE = (-4142, 'none', 'No tick mark')
```

No tick mark

### `OUTSIDE`

```python
OUTSIDE = (3, 'out', 'Tick mark appears outside the axis')
```

Tick mark appears outside the axis
