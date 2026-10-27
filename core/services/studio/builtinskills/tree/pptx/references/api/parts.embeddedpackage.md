<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.parts.embeddedpackage`

Embedded Package part objects.

## `EmbeddedDocxPart`

Bases: `EmbeddedPackagePart`

A Word .docx file stored in a part.

This part-type arises when a Word document appears as an embedded OLE-object shape.

### `content_type`

```python
content_type = CT.WML_DOCUMENT
```

### `partname_template`

```python
partname_template = '/ppt/embeddings/Microsoft_Word_Document%d.docx'
```

## `EmbeddedPackagePart`

Bases: `Part`

A distinct OPC package, e.g. an Excel file, embedded in this PPTX package.

Has a partname like: `ppt/embeddings/Microsoft_Excel_Sheet1.xlsx`.

### `factory`

```python
factory(prog_id: PROG_ID | str, object_blob: bytes, package: Package)
```

Return a new `EmbeddedPackagePart` subclass instance added to *package*.

The subclass is determined by `prog_id` which corresponds to the "application"
used to open the "file-type" of `object_blob`. The returned part contains the
bytes of `object_blob` and has the content-type also determined by `prog_id`.

### `new`

```python
new(blob: bytes, package: Package)
```

Return new `EmbeddedPackagePart` subclass object.

The returned part object contains `blob` and is added to `package`.

## `EmbeddedPptxPart`

Bases: `EmbeddedPackagePart`

A PowerPoint file stored in a part.

This part-type arises when a PowerPoint presentation (.pptx file) appears as an
embedded OLE-object shape.

### `content_type`

```python
content_type = CT.PML_PRESENTATION
```

### `partname_template`

```python
partname_template = '/ppt/embeddings/Microsoft_PowerPoint_Presentation%d.pptx'
```

## `EmbeddedXlsxPart`

Bases: `EmbeddedPackagePart`

An Excel file stored in a part.

This part-type arises as the data source for a chart, but may also be the OLE-object
for an embedded object shape.

### `content_type`

```python
content_type = CT.SML_SHEET
```

### `partname_template`

```python
partname_template = '/ppt/embeddings/Microsoft_Excel_Sheet%d.xlsx'
```
