---
name: xlsx
description: Use when the user wants to read, change or create an Excel workbook (.xlsx) in their files. Works with the Office add-on tools, with no command runner.
---
# Excel workbooks with the Office tools

Use the tools of the Office add-on: `addon__office__read-xlsx`,
`addon__office__edit-xlsx` and `addon__office__create-xlsx`. A file is a
path in the user's files, such as `/reports/plan.xlsx`.

## Read first

1. Call `read-xlsx` with the path only. You get the sheet names and the
   cell values of each sheet, row by row.
2. For a large workbook, call `read-xlsx` again with `sheet` to read one
   sheet.
3. Answer from what you read. Do not guess a value you did not read.

## Change cells

1. Read the sheet first, so that you know the cell addresses.
2. Call `edit-xlsx` with `path`, `sheet` and `cells`. Each cell has a `ref`
   such as `B3` and exactly one of `text`, `number`, `formula` or `clear`.
3. Change only the cells that must change. Do not create the workbook again
   to change a cell.
4. A formula is a string that starts with `=`, such as `=SUM(B2:B9)`. The
   file is not recalculated: say that the value shows when the file is
   opened in a spreadsheet program.
5. To add a sheet, give `addSheet` with its name in the same call.
6. Tell the user the sheet and the range you changed.

## Create a workbook

Use `create-xlsx` only when no file exists. Give each sheet a `name` and
its `rows`; the first row is the header. A value that starts with `=` is a
formula, and a number is stored as a number. If the path is taken, the old
file stays and the new one is saved next to it: give the user the new path.

## When the tools cannot do it

The tools do not make charts, pivot tables, macros, conditional formats or
cell styles. Say so before you start, and offer what the tools can do. Do
not describe a manual method instead.
