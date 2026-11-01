<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.shapes.placeholder`

Placeholder-related objects.

## `BasePlaceholder`

Bases: `Shape`

NOTE: This class is deprecated and will be removed from a future release
along with the properties *idx*, *orient*, *ph_type*, and *sz*. The *idx*
property will be available via the .placeholder_format property. The
others will be accessed directly from the oxml layer as they are only
used for internal purposes.

Base class for placeholder subclasses that differentiate the varying
behaviors of placeholders on a master, layout, and slide.

### `idx`

```python
idx
```

Integer placeholder 'idx' attribute, e.g. 0

### `orient`

```python
orient
```

Placeholder orientation, e.g. ST_Direction.HORZ

### `ph_type`

```python
ph_type
```

Placeholder type, e.g. PP_PLACEHOLDER.CENTER_TITLE

### `sz`

```python
sz
```

Placeholder 'sz' attribute, e.g. ST_PlaceholderSize.FULL

## `ChartPlaceholder`

Bases: `_BaseSlidePlaceholder`

Placeholder shape that can only accept a chart.

### `insert_chart`

```python
insert_chart(chart_type, chart_data)
```

Return a `PlaceholderGraphicFrame` object containing a new chart of
*chart_type* depicting *chart_data* and having the same position and
size as this placeholder. *chart_type* is one of the
`XlChartType` enumeration values. *chart_data* is a `ChartData`
object populated with the categories and series values for the chart.
Note that the new `Chart` object is not returned directly. The chart
object may be accessed using the
`chart` property of the returned
`PlaceholderGraphicFrame` object.

## `LayoutPlaceholder`

Bases: `_InheritsDimensions`, `Shape`

Placeholder shape on a slide layout.

Provides differentiated behavior for slide layout placeholders, in particular, inheriting
shape properties from the master placeholder having the same type, when a matching one exists.

### `element`

```python
element: CT_Shape
```

## `MasterPlaceholder`

Bases: `BasePlaceholder`

Placeholder shape on a slide master.

### `element`

```python
element: CT_Shape
```

## `NotesSlidePlaceholder`

Bases: `_InheritsDimensions`, `Shape`

Placeholder shape on a notes slide. Inherits shape properties from the
placeholder on the notes master that has the same type (e.g. 'body').

## `PicturePlaceholder`

Bases: `_BaseSlidePlaceholder`

Placeholder shape that can only accept a picture.

### `insert_picture`

```python
insert_picture(image_file)
```

Return a `PlaceholderPicture` object depicting the image in `image_file`.

`image_file` may be either a path (string) or a file-like object. The image is
cropped to fill the entire space of the placeholder. A `PlaceholderPicture`
object has all the properties and methods of a `Picture` shape except that the
value of its `shape_type` property is
`MSO_SHAPE_TYPE.PLACEHOLDER` instead of `MSO_SHAPE_TYPE.PICTURE`.

## `PlaceholderGraphicFrame`

Bases: `GraphicFrame`

Placeholder shape populated with a table, chart, or smart art.

### `is_placeholder`

```python
is_placeholder
```

Boolean indicating whether this shape is a placeholder.
Unconditionally `True` in this case.

## `PlaceholderPicture`

Bases: `_InheritsDimensions`, `Picture`

Placeholder shape populated with a picture.

## `SlidePlaceholder`

Bases: `_BaseSlidePlaceholder`

Placeholder shape on a slide. Inherits shape properties from its
corresponding slide layout placeholder.

## `TablePlaceholder`

Bases: `_BaseSlidePlaceholder`

Placeholder shape that can only accept a table.

### `insert_table`

```python
insert_table(rows, cols)
```

Return `PlaceholderGraphicFrame` object containing a `rows` by `cols` table.

The position and width of the table are those of the placeholder and its height
is proportional to the number of rows. A `PlaceholderGraphicFrame` object has
all the properties and methods of a `GraphicFrame` shape except that the value
of its `shape_type` property is unconditionally
`MSO_SHAPE_TYPE.PLACEHOLDER`. Note that the return value is not the new table
but rather *contains* the new table. The table can be accessed using the
`table` property of the returned
`PlaceholderGraphicFrame` object.
