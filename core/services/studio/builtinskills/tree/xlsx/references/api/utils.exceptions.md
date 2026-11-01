<!-- Generated from openpyxl 0.2.1 docstrings by griffe2md. Do not edit: fix the docstring in the source. -->

# `openpyxl.utils.exceptions`

Definitions for openpyxl shared exception classes.

## `CellCoordinatesException`

Bases: `Exception`

Error for converting between numeric and A1-style cell references.

## `IllegalCharacterError`

Bases: `Exception`

The data submitted which cannot be used directly in Excel files. It
must be removed or escaped.

## `InvalidFileException`

Bases: `Exception`

Error for trying to open a non-ooxml file.

## `NamedRangeException`

Bases: `Exception`

Error for badly formatted named ranges.

## `ReadOnlyWorkbookException`

Bases: `Exception`

Error for trying to modify a read-only workbook

## `SheetTitleException`

Bases: `Exception`

Error for bad sheet names.

## `WorkbookAlreadySaved`

Bases: `Exception`

Error when attempting to perform operations on a dump workbook
while it has already been dumped once
