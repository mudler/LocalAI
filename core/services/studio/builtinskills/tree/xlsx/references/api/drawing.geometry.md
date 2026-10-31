<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.geometry`

## `AdjPoint2D`

```python
AdjPoint2D(x = None, y = None)
```

Bases: `Serialisable`

### `x`

```python
x = x
```

### `y`

```python
y = y
```

## `AdjustHandleList`

Bases: `Serialisable`

## `Backdrop`

```python
Backdrop(anchor = None, norm = None, up = None, extLst = None)
```

Bases: `Serialisable`

### `anchor`

```python
anchor = anchor
```

### `extLst`

```python
extLst = extLst
```

### `norm`

```python
norm = norm
```

### `up`

```python
up = up
```

## `Bevel`

```python
Bevel(w = None, h = None, prst = None)
```

Bases: `Serialisable`

### `h`

```python
h = h
```

### `prst`

```python
prst = prst
```

### `tagname`

```python
tagname = 'bevel'
```

### `w`

```python
w = w
```

## `Camera`

```python
Camera(prst = None, fov = None, zoom = None, rot = None)
```

Bases: `Serialisable`

### `fov`

```python
fov = fov
```

### `prst`

```python
prst = prst
```

### `rot`

```python
rot = rot
```

### `tagname`

```python
tagname = 'camera'
```

### `zoom`

```python
zoom = zoom
```

## `ConnectionSite`

```python
ConnectionSite(ang = None, pos = None)
```

Bases: `Serialisable`

### `ang`

```python
ang = ang
```

### `pos`

```python
pos = pos
```

## `ConnectionSiteList`

```python
ConnectionSiteList(cxn = None)
```

Bases: `Serialisable`

### `cxn`

```python
cxn = cxn
```

## `CustomGeometry2D`

```python
CustomGeometry2D(avLst = None, gdLst = None, ahLst = None, cxnLst = None, rect = None, pathLst = None)
```

Bases: `Serialisable`

### `ahLst`

```python
ahLst = ahLst
```

### `avLst`

```python
avLst = avLst
```

### `cxnLst`

```python
cxnLst = cxnLst
```

### `gdLst`

```python
gdLst = gdLst
```

### `pathLst`

```python
pathLst = pathLst
```

### `rect`

```python
rect = None
```

## `FontReference`

```python
FontReference(idx = None)
```

Bases: `Serialisable`

### `idx`

```python
idx = idx
```

## `GeomGuide`

```python
GeomGuide(name = None, fmla = None)
```

Bases: `Serialisable`

### `fmla`

```python
fmla = fmla
```

### `name`

```python
name = name
```

## `GeomGuideList`

```python
GeomGuideList(gd = None)
```

Bases: `Serialisable`

### `gd`

```python
gd = gd
```

## `GeomRect`

```python
GeomRect(l = None, t = None, r = None, b = None)
```

Bases: `Serialisable`

### `b`

```python
b = b
```

### `l`

```python
l = l
```

### `r`

```python
r = r
```

### `t`

```python
t = t
```

## `GroupTransform2D`

```python
GroupTransform2D(rot = 0, flipH = None, flipV = None, off = None, ext = None, chOff = None, chExt = None)
```

Bases: `Serialisable`

### `chExt`

```python
chExt = chExt
```

### `chOff`

```python
chOff = chOff
```

### `ext`

```python
ext = ext
```

### `flipH`

```python
flipH = flipH
```

### `flipV`

```python
flipV = flipV
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `off`

```python
off = off
```

### `rot`

```python
rot = rot
```

### `tagname`

```python
tagname = 'xfrm'
```

## `LightRig`

```python
LightRig(rig = None, dir = None, rot = None)
```

Bases: `Serialisable`

### `dir`

```python
dir = dir
```

### `rig`

```python
rig = rig
```

### `rot`

```python
rot = rot
```

### `tagname`

```python
tagname = 'lightRig'
```

## `Path2D`

```python
Path2D(w = None, h = None, fill = None, stroke = None, extrusionOk = None)
```

Bases: `Serialisable`

### `extrusionOk`

```python
extrusionOk = extrusionOk
```

### `fill`

```python
fill = fill
```

### `h`

```python
h = h
```

### `stroke`

```python
stroke = stroke
```

### `w`

```python
w = w
```

## `Path2DList`

```python
Path2DList(path = None)
```

Bases: `Serialisable`

### `path`

```python
path = path
```

## `Point2D`

```python
Point2D(x = None, y = None)
```

Bases: `Serialisable`

### `namespace`

```python
namespace = DRAWING_NS
```

### `tagname`

```python
tagname = 'off'
```

### `x`

```python
x = x
```

### `y`

```python
y = y
```

## `Point3D`

```python
Point3D(x = None, y = None, z = None)
```

Bases: `Serialisable`

### `tagname`

```python
tagname = 'anchor'
```

### `x`

```python
x = x
```

### `y`

```python
y = y
```

### `z`

```python
z = z
```

## `PositiveSize2D`

```python
PositiveSize2D(cx = None, cy = None)
```

Bases: `Serialisable`

### `cx`

```python
cx = cx
```

### `cy`

```python
cy = cy
```

### `height`

```python
height = Alias('cy')
```

### `namespace`

```python
namespace = DRAWING_NS
```

Dimensions in EMUs

### `tagname`

```python
tagname = 'ext'
```

### `width`

```python
width = Alias('cx')
```

## `PresetGeometry2D`

```python
PresetGeometry2D(prst = None, avLst = None)
```

Bases: `Serialisable`

### `avLst`

```python
avLst = avLst
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `prst`

```python
prst = prst
```

## `Scene3D`

```python
Scene3D(camera = None, lightRig = None, backdrop = None, extLst = None)
```

Bases: `Serialisable`

### `backdrop`

```python
backdrop = backdrop
```

### `camera`

```python
camera = camera
```

### `extLst`

```python
extLst = extLst
```

### `lightRig`

```python
lightRig = lightRig
```

## `Shape3D`

```python
Shape3D(z = None, extrusionH = None, contourW = None, prstMaterial = None, bevelT = None, bevelB = None, extrusionClr = None, contourClr = None, extLst = None)
```

Bases: `Serialisable`

### `bevelB`

```python
bevelB = bevelB
```

### `bevelT`

```python
bevelT = bevelT
```

### `contourClr`

```python
contourClr = contourClr
```

### `contourW`

```python
contourW = contourW
```

### `extLst`

```python
extLst = extLst
```

### `extrusionClr`

```python
extrusionClr = extrusionClr
```

### `extrusionH`

```python
extrusionH = extrusionH
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `prstMaterial`

```python
prstMaterial = prstMaterial
```

### `z`

```python
z = z
```

## `ShapeStyle`

```python
ShapeStyle(lnRef = None, fillRef = None, effectRef = None, fontRef = None)
```

Bases: `Serialisable`

### `effectRef`

```python
effectRef = effectRef
```

### `fillRef`

```python
fillRef = fillRef
```

### `fontRef`

```python
fontRef = fontRef
```

### `lnRef`

```python
lnRef = lnRef
```

## `SphereCoords`

```python
SphereCoords(lat = None, lon = None, rev = None)
```

Bases: `Serialisable`

### `lat`

```python
lat = lat
```

### `lon`

```python
lon = lon
```

### `rev`

```python
rev = rev
```

### `tagname`

```python
tagname = 'sphereCoords'
```

## `StyleMatrixReference`

```python
StyleMatrixReference(idx = None)
```

Bases: `Serialisable`

### `idx`

```python
idx = idx
```

## `Transform2D`

```python
Transform2D(rot = None, flipH = None, flipV = None, off = None, ext = None, chOff = None, chExt = None)
```

Bases: `Serialisable`

### `chExt`

```python
chExt = chExt
```

### `chOff`

```python
chOff = chOff
```

### `ext`

```python
ext = ext
```

### `flipH`

```python
flipH = flipH
```

### `flipV`

```python
flipV = flipV
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `off`

```python
off = off
```

### `rot`

```python
rot = rot
```

### `tagname`

```python
tagname = 'xfrm'
```

## `Vector3D`

```python
Vector3D(dx = None, dy = None, dz = None)
```

Bases: `Serialisable`

### `dx`

```python
dx = dx
```

### `dy`

```python
dy = dy
```

### `dz`

```python
dz = dz
```

### `tagname`

```python
tagname = 'vector'
```
