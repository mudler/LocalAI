<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.fill`

## `Blip`

```python
Blip(cstate = None, embed = None, link = None, noGrp = None, noSelect = None, noRot = None, noChangeAspect = None, noMove = None, noResize = None, noEditPoints = None, noAdjustHandles = None, noChangeArrowheads = None, noChangeShapeType = None, extLst = None, alphaBiLevel = None, alphaCeiling = None, alphaFloor = None, alphaInv = None, alphaMod = None, alphaModFix = None, alphaRepl = None, biLevel = None, blur = None, clrChange = None, clrRepl = None, duotone = None, fillOverlay = None, grayscl = None, hsl = None, lum = None, tint = None)
```

Bases: `Serialisable`

### `alphaBiLevel`

```python
alphaBiLevel = alphaBiLevel
```

### `alphaCeiling`

```python
alphaCeiling = alphaCeiling
```

### `alphaFloor`

```python
alphaFloor = alphaFloor
```

### `alphaInv`

```python
alphaInv = alphaInv
```

### `alphaMod`

```python
alphaMod = alphaMod
```

### `alphaModFix`

```python
alphaModFix = alphaModFix
```

### `alphaRepl`

```python
alphaRepl = alphaRepl
```

### `biLevel`

```python
biLevel = biLevel
```

### `blur`

```python
blur = blur
```

### `clrChange`

```python
clrChange = clrChange
```

### `clrRepl`

```python
clrRepl = clrRepl
```

### `cstate`

```python
cstate = cstate
```

### `duotone`

```python
duotone = duotone
```

### `embed`

```python
embed = embed
```

### `extLst`

```python
extLst = extLst
```

### `fillOverlay`

```python
fillOverlay = fillOverlay
```

### `grayscl`

```python
grayscl = grayscl
```

### `hsl`

```python
hsl = hsl
```

### `link`

```python
link = link
```

### `lum`

```python
lum = lum
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `noAdjustHandles`

```python
noAdjustHandles = noAdjustHandles
```

### `noChangeArrowheads`

```python
noChangeArrowheads = noChangeArrowheads
```

### `noChangeAspect`

```python
noChangeAspect = noChangeAspect
```

### `noChangeShapeType`

```python
noChangeShapeType = noChangeShapeType
```

### `noEditPoints`

```python
noEditPoints = noEditPoints
```

### `noGrp`

```python
noGrp = noGrp
```

### `noMove`

```python
noMove = noMove
```

### `noResize`

```python
noResize = noResize
```

### `noRot`

```python
noRot = noRot
```

### `noSelect`

```python
noSelect = noSelect
```

### `tagname`

```python
tagname = 'blip'
```

### `tint`

```python
tint = tint
```

## `BlipFillProperties`

```python
BlipFillProperties(dpi = None, rotWithShape = None, blip = None, tile = None, stretch = StretchInfoProperties(), srcRect = None)
```

Bases: `Serialisable`

### `blip`

```python
blip = blip
```

### `dpi`

```python
dpi = dpi
```

### `rotWithShape`

```python
rotWithShape = rotWithShape
```

### `srcRect`

```python
srcRect = srcRect
```

### `stretch`

```python
stretch = stretch
```

### `tagname`

```python
tagname = 'blipFill'
```

### `tile`

```python
tile = tile
```

## `GradientFillProperties`

```python
GradientFillProperties(flip = None, rotWithShape = None, gsLst = (), lin = None, path = None, tileRect = None)
```

Bases: `Serialisable`

### `flip`

```python
flip = flip
```

### `gsLst`

```python
gsLst = gsLst
```

### `lin`

```python
lin = lin
```

### `linear`

```python
linear = Alias('lin')
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `path`

```python
path = path
```

### `rotWithShape`

```python
rotWithShape = rotWithShape
```

### `stop_list`

```python
stop_list = Alias('gsLst')
```

### `tagname`

```python
tagname = 'gradFill'
```

### `tileRect`

```python
tileRect = tileRect
```

## `GradientStop`

```python
GradientStop(pos = None, scrgbClr = None, srgbClr = None, hslClr = None, sysClr = None, schemeClr = None, prstClr = None)
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

### `pos`

```python
pos = pos
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
tagname = 'gs'
```

## `LinearShadeProperties`

```python
LinearShadeProperties(ang = None, scaled = None)
```

Bases: `Serialisable`

### `ang`

```python
ang = ang
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `scaled`

```python
scaled = scaled
```

### `tagname`

```python
tagname = 'lin'
```

## `PathShadeProperties`

```python
PathShadeProperties(path = None, fillToRect = None)
```

Bases: `Serialisable`

### `fillToRect`

```python
fillToRect = fillToRect
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `path`

```python
path = path
```

### `tagname`

```python
tagname = 'path'
```

## `PatternFillProperties`

```python
PatternFillProperties(prst = None, fgClr = None, bgClr = None)
```

Bases: `Serialisable`

### `background`

```python
background = Alias('bgClr')
```

### `bgClr`

```python
bgClr = bgClr
```

### `fgClr`

```python
fgClr = fgClr
```

### `foreground`

```python
foreground = Alias('fgClr')
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `preset`

```python
preset = Alias('prst')
```

### `prst`

```python
prst = prst
```

### `tagname`

```python
tagname = 'pattFill'
```

## `RelativeRect`

```python
RelativeRect(l = None, t = None, r = None, b = None)
```

Bases: `Serialisable`

### `b`

```python
b = b
```

### `bottom`

```python
bottom = Alias('b')
```

### `l`

```python
l = l
```

### `left`

```python
left = Alias('l')
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `r`

```python
r = r
```

### `right`

```python
right = Alias('r')
```

### `t`

```python
t = t
```

### `tagname`

```python
tagname = 'rect'
```

### `top`

```python
top = Alias('t')
```

## `SolidColorFillProperties`

```python
SolidColorFillProperties(scrgbClr = None, srgbClr = None, hslClr = None, sysClr = None, schemeClr = None, prstClr = None)
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
tagname = 'solidFill'
```

## `StretchInfoProperties`

```python
StretchInfoProperties(fillRect = RelativeRect())
```

Bases: `Serialisable`

### `fillRect`

```python
fillRect = fillRect
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `tagname`

```python
tagname = 'stretch'
```

## `TileInfoProperties`

```python
TileInfoProperties(tx = None, ty = None, sx = None, sy = None, flip = None, algn = None)
```

Bases: `Serialisable`

### `algn`

```python
algn = algn
```

### `flip`

```python
flip = flip
```

### `sx`

```python
sx = sx
```

### `sy`

```python
sy = sy
```

### `tx`

```python
tx = tx
```

### `ty`

```python
ty = ty
```
