<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.colors`

## `ColorChoice`

```python
ColorChoice(scrgbClr = None, srgbClr = None, hslClr = None, sysClr = None, schemeClr = None, prstClr = None)
```

Bases: `Serialisable`

### `RGB`

```python
RGB = Alias('srgbClr')
```

### `RGBPercent`

```python
RGBPercent = Alias('scrgbClr')
```

### `hslClr`

```python
hslClr = hslClr
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `prstClr`

```python
prstClr = prstClr
```

### `schemeClr`

```python
schemeClr = schemeClr
```

### `scrgbClr`

```python
scrgbClr = scrgbClr
```

### `srgbClr`

```python
srgbClr = srgbClr
```

### `sysClr`

```python
sysClr = sysClr
```

### `tagname`

```python
tagname = 'colorChoice'
```

## `ColorChoiceDescriptor`

Bases: `Typed`

Objects can choose from 7 different kinds of color system.
Assume RGBHex if a string is passed in.

### `allow_none`

```python
allow_none = True
```

### `expected_type`

```python
expected_type = ColorChoice
```

## `ColorMapping`

```python
ColorMapping(bg1 = 'lt1', tx1 = 'dk1', bg2 = 'lt2', tx2 = 'dk2', accent1 = 'accent1', accent2 = 'accent2', accent3 = 'accent3', accent4 = 'accent4', accent5 = 'accent5', accent6 = 'accent6', hlink = 'hlink', folHlink = 'folHlink', extLst = None)
```

Bases: `Serialisable`

### `accent1`

```python
accent1 = accent1
```

### `accent2`

```python
accent2 = accent2
```

### `accent3`

```python
accent3 = accent3
```

### `accent4`

```python
accent4 = accent4
```

### `accent5`

```python
accent5 = accent5
```

### `accent6`

```python
accent6 = accent6
```

### `bg1`

```python
bg1 = bg1
```

### `bg2`

```python
bg2 = bg2
```

### `extLst`

```python
extLst = extLst
```

### `folHlink`

```python
folHlink = folHlink
```

### `hlink`

```python
hlink = hlink
```

### `tagname`

```python
tagname = 'clrMapOvr'
```

### `tx1`

```python
tx1 = tx1
```

### `tx2`

```python
tx2 = tx2
```

## `HSLColor`

```python
HSLColor(hue = None, sat = None, lum = None)
```

Bases: `Serialisable`

### `hue`

```python
hue = hue
```

### `lum`

```python
lum = lum
```

### `sat`

```python
sat = sat
```

### `tagname`

```python
tagname = 'hslClr'
```

## `PRESET_COLORS`

```python
PRESET_COLORS = ['aliceBlue', 'antiqueWhite', 'aqua', 'aquamarine', 'azure', 'beige', 'bisque', 'black', 'blanchedAlmond', 'blue', 'blueViolet', 'brown', 'burlyWood', 'cadetBlue', 'chartreuse', 'chocolate', 'coral', 'cornflowerBlue', 'cornsilk', 'crimson', 'cyan', 'darkBlue', 'darkCyan', 'darkGoldenrod', 'darkGray', 'darkGrey', 'darkGreen', 'darkKhaki', 'darkMagenta', 'darkOliveGreen', 'darkOrange', 'darkOrchid', 'darkRed', 'darkSalmon', 'darkSeaGreen', 'darkSlateBlue', 'darkSlateGray', 'darkSlateGrey', 'darkTurquoise', 'darkViolet', 'dkBlue', 'dkCyan', 'dkGoldenrod', 'dkGray', 'dkGrey', 'dkGreen', 'dkKhaki', 'dkMagenta', 'dkOliveGreen', 'dkOrange', 'dkOrchid', 'dkRed', 'dkSalmon', 'dkSeaGreen', 'dkSlateBlue', 'dkSlateGray', 'dkSlateGrey', 'dkTurquoise', 'dkViolet', 'deepPink', 'deepSkyBlue', 'dimGray', 'dimGrey', 'dodgerBlue', 'firebrick', 'floralWhite', 'forestGreen', 'fuchsia', 'gainsboro', 'ghostWhite', 'gold', 'goldenrod', 'gray', 'grey', 'green', 'greenYellow', 'honeydew', 'hotPink', 'indianRed', 'indigo', 'ivory', 'khaki', 'lavender', 'lavenderBlush', 'lawnGreen', 'lemonChiffon', 'lightBlue', 'lightCoral', 'lightCyan', 'lightGoldenrodYellow', 'lightGray', 'lightGrey', 'lightGreen', 'lightPink', 'lightSalmon', 'lightSeaGreen', 'lightSkyBlue', 'lightSlateGray', 'lightSlateGrey', 'lightSteelBlue', 'lightYellow', 'ltBlue', 'ltCoral', 'ltCyan', 'ltGoldenrodYellow', 'ltGray', 'ltGrey', 'ltGreen', 'ltPink', 'ltSalmon', 'ltSeaGreen', 'ltSkyBlue', 'ltSlateGray', 'ltSlateGrey', 'ltSteelBlue', 'ltYellow', 'lime', 'limeGreen', 'linen', 'magenta', 'maroon', 'medAquamarine', 'medBlue', 'medOrchid', 'medPurple', 'medSeaGreen', 'medSlateBlue', 'medSpringGreen', 'medTurquoise', 'medVioletRed', 'mediumAquamarine', 'mediumBlue', 'mediumOrchid', 'mediumPurple', 'mediumSeaGreen', 'mediumSlateBlue', 'mediumSpringGreen', 'mediumTurquoise', 'mediumVioletRed', 'midnightBlue', 'mintCream', 'mistyRose', 'moccasin', 'navajoWhite', 'navy', 'oldLace', 'olive', 'oliveDrab', 'orange', 'orangeRed', 'orchid', 'paleGoldenrod', 'paleGreen', 'paleTurquoise', 'paleVioletRed', 'papayaWhip', 'peachPuff', 'peru', 'pink', 'plum', 'powderBlue', 'purple', 'red', 'rosyBrown', 'royalBlue', 'saddleBrown', 'salmon', 'sandyBrown', 'seaGreen', 'seaShell', 'sienna', 'silver', 'skyBlue', 'slateBlue', 'slateGray', 'slateGrey', 'snow', 'springGreen', 'steelBlue', 'tan', 'teal', 'thistle', 'tomato', 'turquoise', 'violet', 'wheat', 'white', 'whiteSmoke', 'yellow', 'yellowGreen']
```

## `RGBPercent`

```python
RGBPercent(r = None, g = None, b = None)
```

Bases: `Serialisable`

### `b`

```python
b = b
```

### `g`

```python
g = g
```

### `r`

```python
r = r
```

### `tagname`

```python
tagname = 'rgbClr'
```

## `SCHEME_COLORS`

```python
SCHEME_COLORS = ['bg1', 'tx1', 'bg2', 'tx2', 'accent1', 'accent2', 'accent3', 'accent4', 'accent5', 'accent6', 'hlink', 'folHlink', 'phClr', 'dk1', 'lt1', 'dk2', 'lt2']
```

## `SchemeColor`

```python
SchemeColor(tint = None, shade = None, comp = None, inv = None, gray = None, alpha = None, alphaOff = None, alphaMod = None, hue = None, hueOff = None, hueMod = None, sat = None, satOff = None, satMod = None, lum = None, lumOff = None, lumMod = None, red = None, redOff = None, redMod = None, green = None, greenOff = None, greenMod = None, blue = None, blueOff = None, blueMod = None, gamma = None, invGamma = None, val = None)
```

Bases: `Serialisable`

### `alpha`

```python
alpha = alpha
```

### `alphaMod`

```python
alphaMod = alphaMod
```

### `alphaOff`

```python
alphaOff = alphaOff
```

### `blue`

```python
blue = blue
```

### `blueMod`

```python
blueMod = blueMod
```

### `blueOff`

```python
blueOff = blueOff
```

### `comp`

```python
comp = comp
```

### `gamma`

```python
gamma = gamma
```

### `gray`

```python
gray = gray
```

### `green`

```python
green = green
```

### `greenMod`

```python
greenMod = greenMod
```

### `greenOff`

```python
greenOff = greenOff
```

### `hue`

```python
hue = hue
```

### `hueMod`

```python
hueMod = hueMod
```

### `hueOff`

```python
hueOff = hueOff
```

### `inv`

```python
inv = inv
```

### `invGamma`

```python
invGamma = invGamma
```

### `lum`

```python
lum = lum
```

### `lumMod`

```python
lumMod = lumMod
```

### `lumOff`

```python
lumOff = lumOff
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `red`

```python
red = red
```

### `redMod`

```python
redMod = redMod
```

### `redOff`

```python
redOff = redOff
```

### `sat`

```python
sat = sat
```

### `satMod`

```python
satMod = satMod
```

### `satOff`

```python
satOff = satOff
```

### `shade`

```python
shade = shade
```

### `tagname`

```python
tagname = 'schemeClr'
```

### `tint`

```python
tint = tint
```

### `val`

```python
val = val
```

## `SystemColor`

```python
SystemColor(val = 'windowText', lastClr = None, tint = None, shade = None, comp = None, inv = None, gray = None, alpha = None, alphaOff = None, alphaMod = None, hue = None, hueOff = None, hueMod = None, sat = None, satOff = None, satMod = None, lum = None, lumOff = None, lumMod = None, red = None, redOff = None, redMod = None, green = None, greenOff = None, greenMod = None, blue = None, blueOff = None, blueMod = None, gamma = None, invGamma = None)
```

Bases: `Serialisable`

### `alpha`

```python
alpha = alpha
```

### `alphaMod`

```python
alphaMod = alphaMod
```

### `alphaOff`

```python
alphaOff = alphaOff
```

### `blue`

```python
blue = blue
```

### `blueMod`

```python
blueMod = blueMod
```

### `blueOff`

```python
blueOff = blueOff
```

### `comp`

```python
comp = comp
```

### `gamma`

```python
gamma = gamma
```

### `gray`

```python
gray = gray
```

### `green`

```python
green = green
```

### `greenMod`

```python
greenMod = greenMod
```

### `greenOff`

```python
greenOff = greenOff
```

### `hue`

```python
hue = hue
```

### `hueMod`

```python
hueMod = hueMod
```

### `hueOff`

```python
hueOff = hueOff
```

### `inv`

```python
inv = inv
```

### `invGamma`

```python
invGamma = invGamma
```

### `lastClr`

```python
lastClr = lastClr
```

### `lum`

```python
lum = lum
```

### `lumMod`

```python
lumMod = lumMod
```

### `lumOff`

```python
lumOff = lumOff
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `red`

```python
red = red
```

### `redMod`

```python
redMod = redMod
```

### `redOff`

```python
redOff = redOff
```

### `sat`

```python
sat = sat
```

### `satMod`

```python
satMod = satMod
```

### `satOff`

```python
satOff = satOff
```

### `shade`

```python
shade = shade
```

### `tagname`

```python
tagname = 'sysClr'
```

### `tint`

```python
tint = tint
```

### `val`

```python
val = val
```

## `Transform`

Bases: `Serialisable`
