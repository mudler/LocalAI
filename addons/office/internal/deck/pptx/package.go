package pptx

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/ooxml"
)

// XML namespaces and relationship types.
const (
	xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	nsA       = `http://schemas.openxmlformats.org/drawingml/2006/main`
	nsR       = `http://schemas.openxmlformats.org/officeDocument/2006/relationships`
	nsP       = `http://schemas.openxmlformats.org/presentationml/2006/main`
	nsDecl    = `xmlns:a="` + nsA + `" xmlns:r="` + nsR + `" xmlns:p="` + nsP + `"`

	relBase        = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/`
	relOfficeDoc   = relBase + "officeDocument"
	relCoreProps   = `http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties`
	relExtProps    = relBase + "extended-properties"
	relSlideMaster = relBase + "slideMaster"
	relSlideLayout = relBase + "slideLayout"
	relTheme       = relBase + "theme"
	relSlide       = relBase + "slide"
	relNotesMaster = relBase + "notesMaster"
	relNotesSlide  = relBase + "notesSlide"
	relImage       = relBase + "image"
	relPresProps   = relBase + "presProps"
	relViewProps   = relBase + "viewProps"
	relTableStyles = relBase + "tableStyles"

	ctBase        = `application/vnd.openxmlformats-officedocument.`
	ctPresMain    = ctBase + "presentationml.presentation.main+xml"
	ctSlideMaster = ctBase + "presentationml.slideMaster+xml"
	ctSlideLayout = ctBase + "presentationml.slideLayout+xml"
	ctSlide       = ctBase + "presentationml.slide+xml"
	ctNotesMaster = ctBase + "presentationml.notesMaster+xml"
	ctNotesSlide  = ctBase + "presentationml.notesSlide+xml"
	ctTheme       = ctBase + "theme+xml"
	ctPresProps   = ctBase + "presentationml.presProps+xml"
	ctViewProps   = ctBase + "presentationml.viewProps+xml"
	ctTableStyles = ctBase + "presentationml.tableStyles+xml"
	ctCoreProps   = `application/vnd.openxmlformats-package.core-properties+xml`
	ctExtProps    = ctBase + "extended-properties+xml"
)

// groupHeader opens every shape tree.
const groupHeader = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

const clrMapAttrs = `bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" ` +
	`accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"`

func relsXML(rels []rel) []byte {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, r := range rels {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="%s" Target="%s"/>`, r.id, r.typ, ooxml.Escape(r.target))
	}
	b.WriteString(`</Relationships>`)
	return []byte(b.String())
}

// packageParts lays out every part of the package, [Content_Types].xml first.
func packageParts(opts Options, pal palette, slides []builtSlide, media []media) []part {
	hasNotes := false
	for _, s := range slides {
		if s.notes != "" {
			hasNotes = true
		}
	}

	var ct strings.Builder
	ct.WriteString(xmlHeader)
	ct.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	ct.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	ct.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	ct.WriteString(`<Default Extension="png" ContentType="image/png"/>`)
	ct.WriteString(`<Default Extension="jpeg" ContentType="image/jpeg"/>`)
	override := func(name, typ string) {
		fmt.Fprintf(&ct, `<Override PartName="/%s" ContentType="%s"/>`, name, typ)
	}

	var parts []part
	add := func(name string, data []byte, typ string) {
		parts = append(parts, part{name: name, data: data})
		if typ != "" {
			override(name, typ)
		}
	}

	add("_rels/.rels", relsXML([]rel{
		{"rId1", relOfficeDoc, "ppt/presentation.xml"},
		{"rId2", relCoreProps, "docProps/core.xml"},
		{"rId3", relExtProps, "docProps/app.xml"},
	}), "")
	notesCount := 0
	for _, s := range slides {
		if s.notes != "" {
			notesCount++
		}
	}
	add("docProps/core.xml", coreXML(opts.Title), ctCoreProps)
	add("docProps/app.xml", appXML(len(slides), notesCount, opts.Application), ctExtProps)

	presRels := []rel{
		{"rId1", relSlideMaster, "slideMasters/slideMaster1.xml"},
		{"rId2", relTheme, "theme/theme1.xml"},
		{"rId3", relPresProps, "presProps.xml"},
		{"rId4", relViewProps, "viewProps.xml"},
		{"rId5", relTableStyles, "tableStyles.xml"},
	}
	notesMasterRID := ""
	if hasNotes {
		notesMasterRID = "rId6"
		presRels = append(presRels, rel{notesMasterRID, relNotesMaster, "notesMasters/notesMaster1.xml"})
	}
	slideRIDs := make([]string, len(slides))
	for i := range slides {
		slideRIDs[i] = fmt.Sprintf("rId%d", len(presRels)+1)
		presRels = append(presRels, rel{slideRIDs[i], relSlide, fmt.Sprintf("slides/slide%d.xml", i+1)})
	}
	add("ppt/presentation.xml", presentationXML(slideRIDs, notesMasterRID), ctPresMain)
	add("ppt/_rels/presentation.xml.rels", relsXML(presRels), "")
	add("ppt/presProps.xml", []byte(xmlHeader+`<p:presentationPr `+nsDecl+`/>`), ctPresProps)
	add("ppt/viewProps.xml", []byte(xmlHeader+`<p:viewPr `+nsDecl+`/>`), ctViewProps)
	add("ppt/tableStyles.xml", []byte(xmlHeader+`<a:tblStyleLst xmlns:a="`+nsA+`" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`), ctTableStyles)

	add("ppt/slideMasters/slideMaster1.xml", slideMasterXML(pal), ctSlideMaster)
	add("ppt/slideMasters/_rels/slideMaster1.xml.rels", relsXML([]rel{
		{"rId1", relSlideLayout, "../slideLayouts/slideLayout1.xml"},
		{"rId2", relTheme, "../theme/theme1.xml"},
		{"rId3", relSlideLayout, "../slideLayouts/slideLayout2.xml"},
	}), "")
	add("ppt/slideLayouts/slideLayout1.xml", []byte(xmlHeader+`<p:sldLayout `+nsDecl+` type="blank" preserve="1">`+
		`<p:cSld name="Blank"><p:spTree>`+groupHeader+`</p:spTree></p:cSld>`+
		`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`), ctSlideLayout)
	add("ppt/slideLayouts/_rels/slideLayout1.xml.rels", relsXML([]rel{
		{"rId1", relSlideMaster, "../slideMasters/slideMaster1.xml"},
	}), "")
	add("ppt/slideLayouts/slideLayout2.xml", titleLayoutXML(), ctSlideLayout)
	add("ppt/slideLayouts/_rels/slideLayout2.xml.rels", relsXML([]rel{
		{"rId1", relSlideMaster, "../slideMasters/slideMaster1.xml"},
	}), "")
	add("ppt/theme/theme1.xml", themeXML("Studio", pal), ctTheme)
	if hasNotes {
		add("ppt/theme/theme2.xml", themeXML("Notes", paletteFor("plain")), ctTheme)
		add("ppt/notesMasters/notesMaster1.xml", notesMasterXML(), ctNotesMaster)
		add("ppt/notesMasters/_rels/notesMaster1.xml.rels", relsXML([]rel{
			{"rId1", relTheme, "../theme/theme2.xml"},
		}), "")
	}

	notesN := 0
	for i, s := range slides {
		n := i + 1
		layout := "../slideLayouts/slideLayout1.xml"
		if s.titled {
			layout = "../slideLayouts/slideLayout2.xml"
		}
		rels := append([]rel{{"rId1", relSlideLayout, layout}}, s.rels...)
		if s.notes != "" {
			notesN++
			rels = append(rels, rel{fmt.Sprintf("rId%d", len(rels)+1), relNotesSlide, fmt.Sprintf("../notesSlides/notesSlide%d.xml", notesN)})
			add(fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", notesN), notesSlideXML(s.notes), ctNotesSlide)
			add(fmt.Sprintf("ppt/notesSlides/_rels/notesSlide%d.xml.rels", notesN), relsXML([]rel{
				{"rId1", relNotesMaster, "../notesMasters/notesMaster1.xml"},
				{"rId2", relSlide, fmt.Sprintf("../slides/slide%d.xml", n)},
			}), "")
		}
		add(fmt.Sprintf("ppt/slides/slide%d.xml", n), []byte(s.xml), ctSlide)
		add(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n), relsXML(rels), "")
	}
	for _, m := range media {
		add("ppt/media/"+m.name, m.data, "")
	}

	ct.WriteString(`</Types>`)
	return append([]part{{name: "[Content_Types].xml", data: []byte(ct.String())}}, parts...)
}

func coreXML(title string) []byte {
	return []byte(xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
		`xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + ooxml.Escape(title) + `</dc:title></cp:coreProperties>`)
}

func appXML(slides, notes int, application string) []byte {
	var app string
	if application != "" {
		app = "<Application>" + ooxml.Escape(application) + "</Application>"
	}
	return fmt.Appendf(nil, xmlHeader+`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" `+
		`xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">`+
		`%s<PresentationFormat>Widescreen</PresentationFormat>`+
		`<Slides>%d</Slides><Notes>%d</Notes></Properties>`, app, slides, notes)
}

func presentationXML(slideRIDs []string, notesMasterRID string) []byte {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<p:presentation ` + nsDecl + ` saveSubsetFonts="1">`)
	b.WriteString(`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>`)
	if notesMasterRID != "" {
		b.WriteString(`<p:notesMasterIdLst><p:notesMasterId r:id="` + notesMasterRID + `"/></p:notesMasterIdLst>`)
	}
	if len(slideRIDs) > 0 {
		b.WriteString(`<p:sldIdLst>`)
		for i, rid := range slideRIDs {
			fmt.Fprintf(&b, `<p:sldId id="%d" r:id="%s"/>`, firstSlideID+i, rid)
		}
		b.WriteString(`</p:sldIdLst>`)
	}
	fmt.Fprintf(&b, `<p:sldSz cx="%d" cy="%d"/><p:notesSz cx="6858000" cy="9144000"/>`, slideW, slideH)
	b.WriteString(`<p:defaultTextStyle><a:defPPr><a:defRPr lang="en-US"/></a:defPPr></p:defaultTextStyle>`)
	b.WriteString(`</p:presentation>`)
	return []byte(b.String())
}

// titleLayoutXML is the Title and Content layout: a title placeholder and an
// object placeholder (idx 1) for the body. Both have their place on the slide
// so that a slide added from the layout in an editor is laid out like ours.
func titleLayoutXML() []byte {
	return []byte(xmlHeader + `<p:sldLayout ` + nsDecl + ` type="obj" preserve="1">` +
		`<p:cSld name="Title and Content"><p:spTree>` + groupHeader +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Title 1"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr>` + phTitle + `</p:nvPr></p:nvSpPr>` +
		`<p:spPr>` + xfrm(padSide, padTop, frameW, 12, "") + `</p:spPr>` +
		`<p:txBody><a:bodyPr anchor="b"/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US"/><a:t>Click to edit the title</a:t></a:r><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Content Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr>` + phBody + `</p:nvPr></p:nvSpPr>` +
		`<p:spPr>` + xfrm(padSide, padTop+14, frameW, frameBot-padTop-14, "") + `</p:spPr>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:pPr lvl="0"/><a:r><a:rPr lang="en-US"/><a:t>Click to edit the text</a:t></a:r><a:endParaRPr lang="en-US"/></a:p></p:txBody></p:sp>` +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`)
}

func slideMasterXML(pal palette) []byte {
	return []byte(xmlHeader + `<p:sldMaster ` + nsDecl + `>` +
		`<p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr val="` + pal.bg + `"/></a:solidFill><a:effectLst/></p:bgPr></p:bg>` +
		`<p:spTree>` + groupHeader + `</p:spTree></p:cSld>` +
		`<p:clrMap ` + clrMapAttrs + `/>` +
		`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/><p:sldLayoutId id="2147483650" r:id="rId3"/></p:sldLayoutIdLst>` +
		`<p:txStyles>` +
		`<p:titleStyle><a:lvl1pPr><a:defRPr sz="4400"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mj-lt"/></a:defRPr></a:lvl1pPr></p:titleStyle>` +
		`<p:bodyStyle><a:lvl1pPr><a:defRPr sz="2400"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/></a:defRPr></a:lvl1pPr></p:bodyStyle>` +
		`<p:otherStyle><a:lvl1pPr><a:defRPr sz="1800"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/></a:defRPr></a:lvl1pPr></p:otherStyle>` +
		`</p:txStyles></p:sldMaster>`)
}

func themeXML(name string, pal palette) []byte {
	clr := func(tag, val string) string { return `<a:` + tag + `><a:srgbClr val="` + val + `"/></a:` + tag + `>` }
	fill := `<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>`
	line := `<a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>`
	effect := `<a:effectStyle><a:effectLst/></a:effectStyle>`
	font := func(tag string) string {
		return `<a:` + tag + `><a:latin typeface="` + ooxml.Escape(pal.font) + `"/><a:ea typeface=""/><a:cs typeface=""/></a:` + tag + `>`
	}
	return []byte(xmlHeader + `<a:theme xmlns:a="` + nsA + `" name="` + name + `"><a:themeElements>` +
		`<a:clrScheme name="` + name + `">` +
		clr("dk1", pal.ink) + clr("lt1", pal.bg) + clr("dk2", pal.muted) + clr("lt2", pal.wash) +
		clr("accent1", pal.accent) + clr("accent2", pal.muted) + clr("accent3", pal.wash) +
		clr("accent4", pal.ink) + clr("accent5", pal.accent) + clr("accent6", pal.muted) +
		clr("hlink", pal.accent) + clr("folHlink", pal.muted) +
		`</a:clrScheme>` +
		`<a:fontScheme name="` + name + `">` + font("majorFont") + font("minorFont") + `</a:fontScheme>` +
		`<a:fmtScheme name="` + name + `">` +
		`<a:fillStyleLst>` + fill + fill + fill + `</a:fillStyleLst>` +
		`<a:lnStyleLst>` + line + line + line + `</a:lnStyleLst>` +
		`<a:effectStyleLst>` + effect + effect + effect + `</a:effectStyleLst>` +
		`<a:bgFillStyleLst>` + fill + fill + fill + `</a:bgFillStyleLst>` +
		`</a:fmtScheme></a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`)
}

func notesMasterXML() []byte {
	return []byte(xmlHeader + `<p:notesMaster ` + nsDecl + `>` +
		`<p:cSld><p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg><p:spTree>` + groupHeader +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr>` +
		`<p:nvPr><p:ph type="sldImg" idx="2"/></p:nvPr></p:nvSpPr>` +
		`<p:spPr><a:xfrm><a:off x="685800" y="1143000"/><a:ext cx="5486400" cy="3086100"/></a:xfrm>` +
		`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln w="12700"><a:solidFill><a:prstClr val="black"/></a:solidFill></a:ln></p:spPr></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>` +
		`<p:nvPr><p:ph type="body" sz="quarter" idx="3"/></p:nvPr></p:nvSpPr>` +
		`<p:spPr><a:xfrm><a:off x="685800" y="4400550"/><a:ext cx="5486400" cy="3600450"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>` +
		`<p:txBody><a:bodyPr vert="horz" lIns="91440" tIns="45720" rIns="91440" bIns="45720" rtlCol="0"/><a:lstStyle/>` +
		`<a:p><a:pPr lvl="0"/><a:r><a:rPr lang="en-US"/><a:t>Notes</a:t></a:r></a:p></p:txBody></p:sp>` +
		`</p:spTree></p:cSld>` +
		`<p:clrMap ` + clrMapAttrs + `/>` +
		`<p:notesStyle><a:lvl1pPr marL="0" algn="l" defTabSz="914400" rtl="0" eaLnBrk="1" latinLnBrk="0" hangingPunct="1">` +
		`<a:defRPr sz="1200" kern="1200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/></a:defRPr></a:lvl1pPr></p:notesStyle>` +
		`</p:notesMaster>`)
}

func notesSlideXML(notes string) []byte {
	var paras strings.Builder
	for line := range strings.SplitSeq(notes, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			paras.WriteString(`<a:p><a:endParaRPr lang="en-US"/></a:p>`)
			continue
		}
		paras.WriteString(`<a:p><a:r><a:rPr lang="en-US" dirty="0"/><a:t>` + ooxml.Escape(line) + `</a:t></a:r></a:p>`)
	}
	return []byte(xmlHeader + `<p:notes ` + nsDecl + `><p:cSld><p:spTree>` + groupHeader +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Slide Image Placeholder 1"/><p:cNvSpPr><a:spLocks noGrp="1" noRot="1" noChangeAspect="1"/></p:cNvSpPr>` +
		`<p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr><p:spPr/></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Notes Placeholder 2"/><p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr>` +
		`<p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/>` + paras.String() + `</p:txBody></p:sp>` +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>`)
}
