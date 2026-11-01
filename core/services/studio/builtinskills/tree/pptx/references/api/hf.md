<!-- Generated from pptx 0.2.0 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `pptx.hf`

Footer, date, and slide-number application machinery (paper-pptx addition).

## `DATETIME_FIELD_FORMATS`

```python
DATETIME_FIELD_FORMATS: 'Dict[str, str]' = {'datetime': '%m/%d/%Y', 'datetime1': '%m/%d/%Y', 'datetime2': '%A, %B %d, %Y', 'datetime3': '%d %B %Y', 'datetime4': '%B %d, %Y', 'datetime5': '%d-%b-%y', 'datetime6': '%B %y', 'datetime7': '%b-%y', 'datetime8': '%m/%d/%Y %I:%M %p', 'datetime9': '%m/%d/%Y %I:%M:%S %p', 'datetime10': '%H:%M', 'datetime11': '%H:%M:%S', 'datetime12': '%I:%M %p', 'datetime13': '%I:%M:%S %p'}
```

## `apply_presentation_footers`

```python
apply_presentation_footers(prs: 'Presentation', *, footer: 'Optional[str]' = None, slide_number: bool = False, date_format: 'Optional[str]' = None, fixed_date: 'Optional[str]' = None, skip_title_slides: bool = False, now: 'Optional[datetime]' = None) -> None
```

Apply the complete footer state to every slide (the dialog's "Apply to All").

## `apply_slide_footers`

```python
apply_slide_footers(slide: 'Slide', *, footer: 'Optional[str]' = None, slide_number: bool = False, date_format: 'Optional[str]' = None, fixed_date: 'Optional[str]' = None, now: 'Optional[datetime]' = None) -> None
```

Apply the complete footer state to one slide (the dialog's per-slide "Apply").
