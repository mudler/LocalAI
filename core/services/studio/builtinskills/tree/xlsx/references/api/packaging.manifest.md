<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.packaging.manifest`

File manifest

## `DEFAULT_OVERRIDE`

```python
DEFAULT_OVERRIDE = [Override('/' + ARC_STYLE, STYLES_TYPE), Override('/' + ARC_THEME, THEME_TYPE), Override('/docProps/core.xml', 'application/vnd.openxmlformats-package.core-properties+xml'), Override('/docProps/app.xml', 'application/vnd.openxmlformats-officedocument.extended-properties+xml')]
```

## `DEFAULT_TYPES`

```python
DEFAULT_TYPES = [FileExtension('rels', 'application/vnd.openxmlformats-package.relationships+xml'), FileExtension('xml', 'application/xml')]
```

## `FileExtension`

```python
FileExtension(Extension, ContentType)
```

Bases: `Serialisable`

### `ContentType`

```python
ContentType = ContentType
```

### `Extension`

```python
Extension = Extension
```

### `tagname`

```python
tagname = 'Default'
```

## `Manifest`

```python
Manifest(Default = (), Override = ())
```

Bases: `Serialisable`

### `Default`

```python
Default = Default
```

### `Override`

```python
Override = Override
```

### `append`

```python
append(obj)
```

Add content object to the package manifest
# needs a contract...

### `extensions`

```python
extensions
```

Map content types to file extensions
Skip parts without extensions

### `filenames`

```python
filenames
```

### `find`

```python
find(content_type)
```

Find specific content-type

### `findall`

```python
findall(content_type)
```

Find all elements of a specific content-type

### `path`

```python
path = '[Content_Types].xml'
```

### `tagname`

```python
tagname = 'Types'
```

### `to_tree`

```python
to_tree()
```

Custom serialisation method to allow setting a default namespace

## `Override`

```python
Override(PartName, ContentType)
```

Bases: `Serialisable`

### `ContentType`

```python
ContentType = ContentType
```

### `PartName`

```python
PartName = PartName
```

### `tagname`

```python
tagname = 'Override'
```

## `mimetypes`

```python
mimetypes = MimeTypes()
```
