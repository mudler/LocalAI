<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.effect`

## `AlphaBiLevelEffect`

```python
AlphaBiLevelEffect(thresh = None)
```

Bases: `Serialisable`

### `thresh`

```python
thresh = thresh
```

## `AlphaCeilingEffect`

Bases: `Serialisable`

## `AlphaFloorEffect`

Bases: `Serialisable`

## `AlphaInverseEffect`

Bases: `Serialisable`

## `AlphaModulateEffect`

```python
AlphaModulateEffect(cont = None)
```

Bases: `Serialisable`

### `cont`

```python
cont = cont
```

## `AlphaModulateFixedEffect`

```python
AlphaModulateFixedEffect(amt = None)
```

Bases: `Serialisable`

### `amt`

```python
amt = amt
```

## `AlphaReplaceEffect`

```python
AlphaReplaceEffect(a = None)
```

Bases: `Serialisable`

### `a`

```python
a = a
```

## `BiLevelEffect`

```python
BiLevelEffect(thresh = None)
```

Bases: `Serialisable`

### `thresh`

```python
thresh = thresh
```

## `BlurEffect`

```python
BlurEffect(rad = None, grow = None)
```

Bases: `Serialisable`

### `grow`

```python
grow = grow
```

### `rad`

```python
rad = rad
```

## `Color`

Bases: `Serialisable`

## `ColorChangeEffect`

```python
ColorChangeEffect(useA = None, clrFrom = None, clrTo = None)
```

Bases: `Serialisable`

### `clrFrom`

```python
clrFrom = clrFrom
```

### `clrTo`

```python
clrTo = clrTo
```

### `useA`

```python
useA = useA
```

## `ColorReplaceEffect`

Bases: `Serialisable`

## `DuotoneEffect`

Bases: `Serialisable`

## `EffectContainer`

```python
EffectContainer(type = None, name = None)
```

Bases: `Serialisable`

### `name`

```python
name = name
```

### `type`

```python
type = type
```

## `EffectList`

```python
EffectList(blur = None, fillOverlay = None, glow = None, innerShdw = None, outerShdw = None, prstShdw = None, reflection = None, softEdge = None)
```

Bases: `Serialisable`

### `blur`

```python
blur = blur
```

### `fillOverlay`

```python
fillOverlay = fillOverlay
```

### `glow`

```python
glow = glow
```

### `innerShdw`

```python
innerShdw = innerShdw
```

### `outerShdw`

```python
outerShdw = outerShdw
```

### `prstShdw`

```python
prstShdw = prstShdw
```

### `reflection`

```python
reflection = reflection
```

### `softEdge`

```python
softEdge = softEdge
```

## `FillOverlayEffect`

```python
FillOverlayEffect(blend = None)
```

Bases: `Serialisable`

### `blend`

```python
blend = blend
```

## `GlowEffect`

```python
GlowEffect(rad = None, **kw)
```

Bases: `ColorChoice`

### `hslClr`

```python
hslClr = ColorChoice.hslClr
```

### `prstClr`

```python
prstClr = ColorChoice.prstClr
```

### `rad`

```python
rad = rad
```

### `schemeClr`

```python
schemeClr = ColorChoice.schemeClr
```

### `scrgbClr`

```python
scrgbClr = ColorChoice.scrgbClr
```

### `srgbClr`

```python
srgbClr = ColorChoice.srgbClr
```

### `sysClr`

```python
sysClr = ColorChoice.sysClr
```

## `GrayscaleEffect`

Bases: `Serialisable`

### `tagname`

```python
tagname = 'grayscl'
```

## `HSLEffect`

```python
HSLEffect(hue = None, sat = None, lum = None)
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

## `InnerShadowEffect`

```python
InnerShadowEffect(blurRad = None, dist = None, dir = None, **kw)
```

Bases: `ColorChoice`

### `blurRad`

```python
blurRad = blurRad
```

### `dir`

```python
dir = dir
```

### `dist`

```python
dist = dist
```

### `hslClr`

```python
hslClr = ColorChoice.hslClr
```

### `prstClr`

```python
prstClr = ColorChoice.prstClr
```

### `schemeClr`

```python
schemeClr = ColorChoice.schemeClr
```

### `scrgbClr`

```python
scrgbClr = ColorChoice.scrgbClr
```

### `srgbClr`

```python
srgbClr = ColorChoice.srgbClr
```

### `sysClr`

```python
sysClr = ColorChoice.sysClr
```

## `LuminanceEffect`

```python
LuminanceEffect(bright = 0, contrast = 0)
```

Bases: `Serialisable`

### `bright`

```python
bright = bright
```

### `contrast`

```python
contrast = contrast
```

### `tagname`

```python
tagname = 'lum'
```

## `OuterShadow`

```python
OuterShadow(blurRad = None, dist = None, dir = None, sx = None, sy = None, kx = None, ky = None, algn = None, rotWithShape = None, **kw)
```

Bases: `ColorChoice`

### `algn`

```python
algn = algn
```

### `blurRad`

```python
blurRad = blurRad
```

### `dir`

```python
dir = dir
```

### `dist`

```python
dist = dist
```

### `hslClr`

```python
hslClr = ColorChoice.hslClr
```

### `kx`

```python
kx = kx
```

### `ky`

```python
ky = ky
```

### `prstClr`

```python
prstClr = ColorChoice.prstClr
```

### `rotWithShape`

```python
rotWithShape = rotWithShape
```

### `schemeClr`

```python
schemeClr = ColorChoice.schemeClr
```

### `scrgbClr`

```python
scrgbClr = ColorChoice.scrgbClr
```

### `srgbClr`

```python
srgbClr = ColorChoice.srgbClr
```

### `sx`

```python
sx = sx
```

### `sy`

```python
sy = sy
```

### `sysClr`

```python
sysClr = ColorChoice.sysClr
```

### `tagname`

```python
tagname = 'outerShdw'
```

## `PresetShadowEffect`

```python
PresetShadowEffect(prst = None, dist = None, dir = None, **kw)
```

Bases: `ColorChoice`

### `dir`

```python
dir = dir
```

### `dist`

```python
dist = dist
```

### `hslClr`

```python
hslClr = ColorChoice.hslClr
```

### `prst`

```python
prst = prst
```

### `prstClr`

```python
prstClr = ColorChoice.prstClr
```

### `schemeClr`

```python
schemeClr = ColorChoice.schemeClr
```

### `scrgbClr`

```python
scrgbClr = ColorChoice.scrgbClr
```

### `srgbClr`

```python
srgbClr = ColorChoice.srgbClr
```

### `sysClr`

```python
sysClr = ColorChoice.sysClr
```

## `ReflectionEffect`

```python
ReflectionEffect(blurRad = None, stA = None, stPos = None, endA = None, endPos = None, dist = None, dir = None, fadeDir = None, sx = None, sy = None, kx = None, ky = None, algn = None, rotWithShape = None)
```

Bases: `Serialisable`

### `algn`

```python
algn = algn
```

### `blurRad`

```python
blurRad = blurRad
```

### `dir`

```python
dir = dir
```

### `dist`

```python
dist = dist
```

### `endA`

```python
endA = endA
```

### `endPos`

```python
endPos = endPos
```

### `fadeDir`

```python
fadeDir = fadeDir
```

### `kx`

```python
kx = kx
```

### `ky`

```python
ky = ky
```

### `rotWithShape`

```python
rotWithShape = rotWithShape
```

### `stA`

```python
stA = stA
```

### `stPos`

```python
stPos = stPos
```

### `sx`

```python
sx = sx
```

### `sy`

```python
sy = sy
```

## `SoftEdgesEffect`

```python
SoftEdgesEffect(rad = None)
```

Bases: `Serialisable`

### `rad`

```python
rad = rad
```

## `TintEffect`

```python
TintEffect(hue = 0, amt = 0)
```

Bases: `Serialisable`

### `amt`

```python
amt = amt
```

### `hue`

```python
hue = hue
```

### `tagname`

```python
tagname = 'tint'
```
