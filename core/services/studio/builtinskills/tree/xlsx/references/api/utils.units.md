<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.utils.units`

## `BASE_COL_WIDTH`

```python
BASE_COL_WIDTH = 8
```

## `DEFAULT_COLUMN_WIDTH`

```python
DEFAULT_COLUMN_WIDTH = BASE_COL_WIDTH + 5
```

## `DEFAULT_HEADER`

```python
DEFAULT_HEADER = 0.3
```

From the ECMA Spec (4th Edition part 1)
Page setup: "Left Page Margin in inches" p. 1647

Docs from
http://startbigthinksmall.wordpress.com/2010/01/04/points-inches-and-emus-measuring-units-in-office-open-xml/

See also http://msdn.microsoft.com/en-us/library/dd560821(v=office.12).aspx

dxa: The main unit in OOXML is a twentieth of a point. Also called twips.
pt: point. In Excel there are 72 points to an inch
hp: half-points are used to specify font sizes. A font-size of 12pt equals 24 half points
pct: Half-points are used to specify font sizes. A font-size of 12pt equals 24 half points

EMU: English Metric Unit, EMUs are used for coordinates in vector-based
drawings and embedded pictures. One inch equates to 914400 EMUs and a
centimeter is 360000. For bitmaps the default resolution is 96 dpi (known as
PixelsPerInch in Excel). Spec p. 1122

For radial geometry Excel uses integer units of 1/60000th of a degree.

## `DEFAULT_LEFT_MARGIN`

```python
DEFAULT_LEFT_MARGIN = 0.7
```

## `DEFAULT_ROW_HEIGHT`

```python
DEFAULT_ROW_HEIGHT = 15.0
```

## `DEFAULT_TOP_MARGIN`

```python
DEFAULT_TOP_MARGIN = 0.7874
```

## `EMU_to_cm`

```python
EMU_to_cm(value)
```

## `EMU_to_inch`

```python
EMU_to_inch(value)
```

## `EMU_to_pixels`

```python
EMU_to_pixels(value)
```

## `angle_to_degrees`

```python
angle_to_degrees(value)
```

## `cm_to_EMU`

```python
cm_to_EMU(value)
```

1 cm = 360000 EMUs

## `cm_to_dxa`

```python
cm_to_dxa(value)
```

## `degrees_to_angle`

```python
degrees_to_angle(value)
```

1 degree = 60000 angles

## `dxa_to_cm`

```python
dxa_to_cm(value)
```

## `dxa_to_inch`

```python
dxa_to_inch(value)
```

## `inch_to_EMU`

```python
inch_to_EMU(value)
```

1 inch = 914400 EMUs

## `inch_to_dxa`

```python
inch_to_dxa(value)
```

1 inch = 72 * 20 dxa

## `pixels_to_EMU`

```python
pixels_to_EMU(value)
```

1 pixel = 9525 EMUs

## `pixels_to_points`

```python
pixels_to_points(value, dpi = 96)
```

96 dpi, 72i

## `points_to_pixels`

```python
points_to_pixels(value, dpi = 96)
```

## `short_color`

```python
short_color(color)
```

format a color to its short size
