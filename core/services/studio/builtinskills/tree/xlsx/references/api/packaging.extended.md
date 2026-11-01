<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.packaging.extended`

## `DigSigBlob`

Bases: `Serialisable`

## `ExtendedProperties`

```python
ExtendedProperties(Template = None, Manager = None, Company = None, Pages = None, Words = None, Characters = None, PresentationFormat = None, Lines = None, Paragraphs = None, Slides = None, Notes = None, TotalTime = None, HiddenSlides = None, MMClips = None, ScaleCrop = None, HeadingPairs = None, TitlesOfParts = None, LinksUpToDate = None, CharactersWithSpaces = None, SharedDoc = None, HyperlinkBase = None, HLinks = None, HyperlinksChanged = None, DigSig = None, Application = None, AppVersion = None, DocSecurity = None)
```

Bases: `Serialisable`

See 22.2

Most of this is irrelevant but Excel is very picky about the version number

It uses XX.YYYY (Version.Build) and expects everyone else to

We provide Major.Minor and the full version in the application name

### `AppVersion`

```python
AppVersion = '.'.join(__version__.split('.')[:-1])
```

### `Application`

```python
Application = f'Microsoft Excel Compatible / Openpyxl {__version__}'
```

### `Characters`

```python
Characters = Characters
```

### `CharactersWithSpaces`

```python
CharactersWithSpaces = CharactersWithSpaces
```

### `Company`

```python
Company = Company
```

### `DigSig`

```python
DigSig = None
```

### `DocSecurity`

```python
DocSecurity = DocSecurity
```

### `HLinks`

```python
HLinks = None
```

### `HeadingPairs`

```python
HeadingPairs = None
```

### `HiddenSlides`

```python
HiddenSlides = HiddenSlides
```

### `HyperlinkBase`

```python
HyperlinkBase = HyperlinkBase
```

### `HyperlinksChanged`

```python
HyperlinksChanged = HyperlinksChanged
```

### `Lines`

```python
Lines = Lines
```

### `LinksUpToDate`

```python
LinksUpToDate = LinksUpToDate
```

### `MMClips`

```python
MMClips = MMClips
```

### `Manager`

```python
Manager = Manager
```

### `Notes`

```python
Notes = Notes
```

### `Pages`

```python
Pages = Pages
```

### `Paragraphs`

```python
Paragraphs = Paragraphs
```

### `PresentationFormat`

```python
PresentationFormat = PresentationFormat
```

### `ScaleCrop`

```python
ScaleCrop = ScaleCrop
```

### `SharedDoc`

```python
SharedDoc = SharedDoc
```

### `Slides`

```python
Slides = Slides
```

### `Template`

```python
Template = Template
```

### `TitlesOfParts`

```python
TitlesOfParts = None
```

### `TotalTime`

```python
TotalTime = TotalTime
```

### `Words`

```python
Words = Words
```

### `tagname`

```python
tagname = 'Properties'
```

### `to_tree`

```python
to_tree()
```

## `VectorLpstr`

Bases: `Serialisable`

## `VectorVariant`

Bases: `Serialisable`
