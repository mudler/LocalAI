<!-- Generated from docx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `docx.oxml.styles`

Custom element classes related to the styles part.

## `CT_LatentStyles`

Bases: `BaseOxmlElement`

`w:latentStyles` element, defining behavior defaults for latent styles and
containing `w:lsdException` child elements that each override those defaults for a
named latent style.

### `bool_prop`

```python
bool_prop(attr_name)
```

Return the boolean value of the attribute having `attr_name`, or `False` if
not present.

### `count`

```python
count = OptionalAttribute('w:count', ST_DecimalNumber)
```

### `defLockedState`

```python
defLockedState = OptionalAttribute('w:defLockedState', ST_OnOff)
```

### `defQFormat`

```python
defQFormat = OptionalAttribute('w:defQFormat', ST_OnOff)
```

### `defSemiHidden`

```python
defSemiHidden = OptionalAttribute('w:defSemiHidden', ST_OnOff)
```

### `defUIPriority`

```python
defUIPriority = OptionalAttribute('w:defUIPriority', ST_DecimalNumber)
```

### `defUnhideWhenUsed`

```python
defUnhideWhenUsed = OptionalAttribute('w:defUnhideWhenUsed', ST_OnOff)
```

### `get_by_name`

```python
get_by_name(name)
```

Return the `w:lsdException` child having `name`, or `None` if not found.

### `lsdException`

```python
lsdException = ZeroOrMore('w:lsdException', successors=())
```

### `set_bool_prop`

```python
set_bool_prop(attr_name, value)
```

Set the on/off attribute having `attr_name` to `value`.

## `CT_LsdException`

Bases: `BaseOxmlElement`

``<w:lsdException>`` element, defining override visibility behaviors for a named
latent style.

### `delete`

```python
delete()
```

Remove this `w:lsdException` element from the XML document.

### `locked`

```python
locked = OptionalAttribute('w:locked', ST_OnOff)
```

### `name`

```python
name = RequiredAttribute('w:name', ST_String)
```

### `on_off_prop`

```python
on_off_prop(attr_name)
```

Return the boolean value of the attribute having `attr_name`, or `None` if
not present.

### `qFormat`

```python
qFormat = OptionalAttribute('w:qFormat', ST_OnOff)
```

### `semiHidden`

```python
semiHidden = OptionalAttribute('w:semiHidden', ST_OnOff)
```

### `set_on_off_prop`

```python
set_on_off_prop(attr_name, value)
```

Set the on/off attribute having `attr_name` to `value`.

### `uiPriority`

```python
uiPriority = OptionalAttribute('w:uiPriority', ST_DecimalNumber)
```

### `unhideWhenUsed`

```python
unhideWhenUsed = OptionalAttribute('w:unhideWhenUsed', ST_OnOff)
```

## `CT_Style`

Bases: `BaseOxmlElement`

A ``<w:style>`` element, representing a style definition.

### `base_style`

```python
base_style
```

Sibling CT_Style element this style is based on or `None` if no base style or
base style not found.

### `basedOn`

```python
basedOn = ZeroOrOne('w:basedOn', successors=_tag_seq[3:])
```

### `basedOn_val`

```python
basedOn_val
```

Value of `w:basedOn/@w:val` or `None` if not present.

### `customStyle`

```python
customStyle = OptionalAttribute('w:customStyle', ST_OnOff)
```

### `default`

```python
default = OptionalAttribute('w:default', ST_OnOff)
```

### `delete`

```python
delete()
```

Remove this `w:style` element from its parent `w:styles` element.

### `locked`

```python
locked = ZeroOrOne('w:locked', successors=_tag_seq[12:])
```

### `locked_val`

```python
locked_val
```

Value of `w:locked/@w:val` or `False` if not present.

### `name`

```python
name = ZeroOrOne('w:name', successors=_tag_seq[1:])
```

### `name_val`

```python
name_val
```

Value of ``<w:name>`` child or `None` if not present.

### `next`

```python
next = ZeroOrOne('w:next', successors=_tag_seq[4:])
```

### `next_style`

```python
next_style
```

Sibling CT_Style element identified by the value of `w:name/@w:val` or `None`
if no value is present or no style with that style id is found.

### `pPr`

```python
pPr = ZeroOrOne('w:pPr', successors=_tag_seq[17:])
```

### `qFormat`

```python
qFormat = ZeroOrOne('w:qFormat', successors=_tag_seq[11:])
```

### `qFormat_val`

```python
qFormat_val
```

Value of `w:qFormat/@w:val` or `False` if not present.

### `rPr`

```python
rPr = ZeroOrOne('w:rPr', successors=_tag_seq[18:])
```

### `semiHidden`

```python
semiHidden = ZeroOrOne('w:semiHidden', successors=_tag_seq[9:])
```

### `semiHidden_val`

```python
semiHidden_val
```

Value of ``<w:semiHidden>`` child or `False` if not present.

### `styleId`

```python
styleId: str | None = OptionalAttribute('w:styleId', ST_String)
```

### `type`

```python
type: WD_STYLE_TYPE | None = OptionalAttribute('w:type', WD_STYLE_TYPE)
```

### `uiPriority`

```python
uiPriority = ZeroOrOne('w:uiPriority', successors=_tag_seq[8:])
```

### `uiPriority_val`

```python
uiPriority_val
```

Value of ``<w:uiPriority>`` child or `None` if not present.

### `unhideWhenUsed`

```python
unhideWhenUsed = ZeroOrOne('w:unhideWhenUsed', successors=_tag_seq[10:])
```

### `unhideWhenUsed_val`

```python
unhideWhenUsed_val
```

Value of `w:unhideWhenUsed/@w:val` or `False` if not present.

## `CT_Styles`

Bases: `BaseOxmlElement`

``<w:styles>`` element, the root element of a styles part, i.e. styles.xml.

### `add_style_of_type`

```python
add_style_of_type(name, style_type, builtin)
```

Return a newly added `w:style` element having `name` and `style_type`.

`w:style/@customStyle` is set based on the value of `builtin`.

### `default_for`

```python
default_for(style_type)
```

Return `w:style[@w:type="*{style_type}*][-1]` or `None` if not found.

### `get_by_id`

```python
get_by_id(styleId: str) -> CT_Style | None
```

`w:style` child where @styleId = `styleId`.

`None` if not found.

### `get_by_name`

```python
get_by_name(name: str) -> CT_Style | None
```

`w:style` child with `w:name` grandchild having value `name`.

`None` if not found.

### `latentStyles`

```python
latentStyles = ZeroOrOne('w:latentStyles', successors=_tag_seq[2:])
```

### `style`

```python
style = ZeroOrMore('w:style', successors=())
```

## `styleId_from_name`

```python
styleId_from_name(name)
```

Return the style id corresponding to `name`, taking into account special-case
names such as 'Heading 1'.
