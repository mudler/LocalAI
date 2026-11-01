<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.drawing.text`

## `AutonumberBullet`

```python
AutonumberBullet(type = None, startAt = None)
```

Bases: `Serialisable`

### `startAt`

```python
startAt = startAt
```

### `type`

```python
type = type
```

## `CharacterProperties`

```python
CharacterProperties(kumimoji = None, lang = None, altLang = None, sz = None, b = None, i = None, u = None, strike = None, kern = None, cap = None, spc = None, normalizeH = None, baseline = None, noProof = None, dirty = None, err = None, smtClean = None, smtId = None, bmk = None, ln = None, highlight = None, latin = None, ea = None, cs = None, sym = None, hlinkClick = None, hlinkMouseOver = None, rtl = None, extLst = None, noFill = None, solidFill = None, gradFill = None, blipFill = None, pattFill = None, grpFill = None, effectLst = None, effectDag = None, uLnTx = None, uLn = None, uFillTx = None, uFill = None)
```

Bases: `Serialisable`

### `altLang`

```python
altLang = altLang
```

### `b`

```python
b = b
```

### `baseline`

```python
baseline = baseline
```

### `blipFill`

```python
blipFill = blipFill
```

### `bmk`

```python
bmk = bmk
```

### `cap`

```python
cap = cap
```

### `cs`

```python
cs = cs
```

### `dirty`

```python
dirty = dirty
```

### `ea`

```python
ea = ea
```

### `effectDag`

```python
effectDag = effectDag
```

### `effectLst`

```python
effectLst = effectLst
```

### `err`

```python
err = err
```

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `gradFill`

```python
gradFill = gradFill
```

### `grpFill`

```python
grpFill = grpFill
```

### `highlight`

```python
highlight = highlight
```

### `hlinkClick`

```python
hlinkClick = hlinkClick
```

### `hlinkMouseOver`

```python
hlinkMouseOver = hlinkMouseOver
```

### `i`

```python
i = i
```

### `kern`

```python
kern = kern
```

### `kumimoji`

```python
kumimoji = kumimoji
```

### `lang`

```python
lang = lang
```

### `latin`

```python
latin = latin
```

### `ln`

```python
ln = ln
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `noFill`

```python
noFill = noFill
```

### `noProof`

```python
noProof = noProof
```

### `normalizeH`

```python
normalizeH = normalizeH
```

### `pattFill`

```python
pattFill = pattFill
```

### `rtl`

```python
rtl = rtl
```

### `smtClean`

```python
smtClean = smtClean
```

### `smtId`

```python
smtId = smtId
```

### `solidFill`

```python
solidFill = solidFill
```

### `spc`

```python
spc = spc
```

### `strike`

```python
strike = strike
```

### `sym`

```python
sym = sym
```

### `sz`

```python
sz = sz
```

### `tagname`

```python
tagname = 'defRPr'
```

### `u`

```python
u = u
```

### `uFill`

```python
uFill = uFill
```

### `uFillTx`

```python
uFillTx = uFillTx
```

### `uLn`

```python
uLn = uLn
```

### `uLnTx`

```python
uLnTx = uLnTx
```

## `EmbeddedWAVAudioFile`

```python
EmbeddedWAVAudioFile(name = None)
```

Bases: `Serialisable`

### `name`

```python
name = name
```

## `Font`

```python
Font(typeface = None, panose = None, pitchFamily = None, charset = None)
```

Bases: `Serialisable`

### `charset`

```python
charset = charset
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `panose`

```python
panose = panose
```

### `pitchFamily`

```python
pitchFamily = pitchFamily
```

### `tagname`

```python
tagname = 'latin'
```

### `typeface`

```python
typeface = typeface
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

## `Hyperlink`

```python
Hyperlink(invalidUrl = None, action = None, tgtFrame = None, tooltip = None, history = None, highlightClick = None, endSnd = None, snd = None, extLst = None, id = None)
```

Bases: `Serialisable`

### `action`

```python
action = action
```

### `endSnd`

```python
endSnd = endSnd
```

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `highlightClick`

```python
highlightClick = highlightClick
```

### `history`

```python
history = history
```

### `id`

```python
id = id
```

### `invalidUrl`

```python
invalidUrl = invalidUrl
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `snd`

```python
snd = snd
```

### `tagname`

```python
tagname = 'hlinkClick'
```

### `tgtFrame`

```python
tgtFrame = tgtFrame
```

### `tooltip`

```python
tooltip = tooltip
```

## `LineBreak`

```python
LineBreak(rPr = None)
```

Bases: `Serialisable`

### `namespace`

```python
namespace = DRAWING_NS
```

### `rPr`

```python
rPr = rPr
```

### `tagname`

```python
tagname = 'br'
```

## `ListStyle`

```python
ListStyle(defPPr = None, lvl1pPr = None, lvl2pPr = None, lvl3pPr = None, lvl4pPr = None, lvl5pPr = None, lvl6pPr = None, lvl7pPr = None, lvl8pPr = None, lvl9pPr = None, extLst = None)
```

Bases: `Serialisable`

### `defPPr`

```python
defPPr = defPPr
```

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `lvl1pPr`

```python
lvl1pPr = lvl1pPr
```

### `lvl2pPr`

```python
lvl2pPr = lvl2pPr
```

### `lvl3pPr`

```python
lvl3pPr = lvl3pPr
```

### `lvl4pPr`

```python
lvl4pPr = lvl4pPr
```

### `lvl5pPr`

```python
lvl5pPr = lvl5pPr
```

### `lvl6pPr`

```python
lvl6pPr = lvl6pPr
```

### `lvl7pPr`

```python
lvl7pPr = lvl7pPr
```

### `lvl8pPr`

```python
lvl8pPr = lvl8pPr
```

### `lvl9pPr`

```python
lvl9pPr = lvl9pPr
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `tagname`

```python
tagname = 'lstStyle'
```

## `Paragraph`

```python
Paragraph(pPr = None, endParaRPr = None, r = None, br = None, fld = None)
```

Bases: `Serialisable`

### `br`

```python
br = br
```

### `endParaRPr`

```python
endParaRPr = endParaRPr
```

### `fld`

```python
fld = fld
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `pPr`

```python
pPr = pPr
```

### `properties`

```python
properties = Alias('pPr')
```

### `r`

```python
r = r
```

### `tagname`

```python
tagname = 'p'
```

### `text`

```python
text = Alias('r')
```

## `ParagraphProperties`

```python
ParagraphProperties(marL = None, marR = None, lvl = None, indent = None, algn = None, defTabSz = None, rtl = None, eaLnBrk = None, fontAlgn = None, latinLnBrk = None, hangingPunct = None, lnSpc = None, spcBef = None, spcAft = None, tabLst = None, defRPr = None, extLst = None, buClrTx = None, buClr = None, buSzTx = None, buSzPct = None, buSzPts = None, buFontTx = None, buFont = None, buNone = None, buAutoNum = None, buChar = None, buBlip = None)
```

Bases: `Serialisable`

### `algn`

```python
algn = algn
```

### `buAutoNum`

```python
buAutoNum = buAutoNum
```

### `buBlip`

```python
buBlip = buBlip
```

### `buChar`

```python
buChar = buChar
```

### `buClr`

```python
buClr = buClr
```

### `buClrTx`

```python
buClrTx = buClrTx
```

### `buFont`

```python
buFont = buFont
```

### `buFontTx`

```python
buFontTx = buFontTx
```

### `buNone`

```python
buNone = buNone
```

### `buSzPct`

```python
buSzPct = buSzPct
```

### `buSzPts`

```python
buSzPts = buSzPts
```

### `buSzTx`

```python
buSzTx = buSzTx
```

### `defRPr`

```python
defRPr = defRPr
```

### `defTabSz`

```python
defTabSz = defTabSz
```

### `eaLnBrk`

```python
eaLnBrk = eaLnBrk
```

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `fontAlgn`

```python
fontAlgn = fontAlgn
```

### `hangingPunct`

```python
hangingPunct = hangingPunct
```

### `indent`

```python
indent = indent
```

### `latinLnBrk`

```python
latinLnBrk = latinLnBrk
```

### `lnSpc`

```python
lnSpc = lnSpc
```

### `lvl`

```python
lvl = lvl
```

### `marL`

```python
marL = marL
```

### `marR`

```python
marR = marR
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `rtl`

```python
rtl = rtl
```

### `spcAft`

```python
spcAft = spcAft
```

### `spcBef`

```python
spcBef = spcBef
```

### `tabLst`

```python
tabLst = tabLst
```

### `tagname`

```python
tagname = 'pPr'
```

## `PresetTextShape`

```python
PresetTextShape(prst = None, avLst = None)
```

Bases: `Serialisable`

### `avLst`

```python
avLst = avLst
```

### `prst`

```python
prst = prst
```

## `RegularTextRun`

```python
RegularTextRun(rPr = None, t = '')
```

Bases: `Serialisable`

### `namespace`

```python
namespace = DRAWING_NS
```

### `properties`

```python
properties = Alias('rPr')
```

### `rPr`

```python
rPr = rPr
```

### `t`

```python
t = t
```

### `tagname`

```python
tagname = 'r'
```

### `value`

```python
value = Alias('t')
```

## `RichTextProperties`

```python
RichTextProperties(rot = None, spcFirstLastPara = None, vertOverflow = None, horzOverflow = None, vert = None, wrap = None, lIns = None, tIns = None, rIns = None, bIns = None, numCol = None, spcCol = None, rtlCol = None, fromWordArt = None, anchor = None, anchorCtr = None, forceAA = None, upright = None, compatLnSpc = None, prstTxWarp = None, scene3d = None, extLst = None, noAutofit = None, normAutofit = None, spAutoFit = None, flatTx = None)
```

Bases: `Serialisable`

### `anchor`

```python
anchor = anchor
```

### `anchorCtr`

```python
anchorCtr = anchorCtr
```

### `bIns`

```python
bIns = bIns
```

### `compatLnSpc`

```python
compatLnSpc = compatLnSpc
```

### `extLst`

```python
extLst = Typed(expected_type=OfficeArtExtensionList, allow_none=True)
```

### `flatTx`

```python
flatTx = flatTx
```

### `forceAA`

```python
forceAA = forceAA
```

### `fromWordArt`

```python
fromWordArt = fromWordArt
```

### `horzOverflow`

```python
horzOverflow = horzOverflow
```

### `lIns`

```python
lIns = lIns
```

### `namespace`

```python
namespace = DRAWING_NS
```

### `noAutofit`

```python
noAutofit = noAutofit
```

### `normAutofit`

```python
normAutofit = normAutofit
```

### `numCol`

```python
numCol = numCol
```

### `prstTxWarp`

```python
prstTxWarp = prstTxWarp
```

### `rIns`

```python
rIns = rIns
```

### `rot`

```python
rot = rot
```

### `rtlCol`

```python
rtlCol = rtlCol
```

### `scene3d`

```python
scene3d = scene3d
```

### `spAutoFit`

```python
spAutoFit = spAutoFit
```

### `spcCol`

```python
spcCol = spcCol
```

### `spcFirstLastPara`

```python
spcFirstLastPara = spcFirstLastPara
```

### `tIns`

```python
tIns = tIns
```

### `tagname`

```python
tagname = 'bodyPr'
```

### `upright`

```python
upright = upright
```

### `vert`

```python
vert = vert
```

### `vertOverflow`

```python
vertOverflow = vertOverflow
```

### `wrap`

```python
wrap = wrap
```

## `Spacing`

```python
Spacing(spcPct = None, spcPts = None)
```

Bases: `Serialisable`

### `spcPct`

```python
spcPct = spcPct
```

### `spcPts`

```python
spcPts = spcPts
```

## `TabStop`

```python
TabStop(pos = None, algn = None)
```

Bases: `Serialisable`

### `algn`

```python
algn = algn
```

### `pos`

```python
pos = pos
```

## `TabStopList`

```python
TabStopList(tab = None)
```

Bases: `Serialisable`

### `tab`

```python
tab = tab
```

## `TextField`

```python
TextField(id = None, type = None, rPr = None, pPr = None, t = None)
```

Bases: `Serialisable`

### `id`

```python
id = id
```

### `pPr`

```python
pPr = pPr
```

### `rPr`

```python
rPr = rPr
```

### `t`

```python
t = t
```

### `type`

```python
type = type
```

## `TextNormalAutofit`

```python
TextNormalAutofit(fontScale = None, lnSpcReduction = None)
```

Bases: `Serialisable`

### `fontScale`

```python
fontScale = fontScale
```

### `lnSpcReduction`

```python
lnSpcReduction = lnSpcReduction
```
