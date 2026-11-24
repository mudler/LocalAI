// Reads a workbook in the browser into the grid the table view draws. The
// reader (SheetJS) loads on first use, so it is not in the main bundle.
import { SHEET_MAX_ROWS } from './officePreview.js'
import { gridOfWorkbook } from './officeGrid.js'

export async function readWorkbookGrid(buffer) {
  const XLSX = await import('xlsx')
  // sheetStubs keeps a formula that has no stored result (a file written by a
  // script), which shows as the formula. sheetRows stops the reader after the
  // rows that are shown; the sheet keeps its full range in !fullref, so the
  // totals are still right.
  const wb = XLSX.read(buffer, { type: 'array', cellDates: true, sheetRows: SHEET_MAX_ROWS, sheetStubs: true, bookVBA: false })
  return gridOfWorkbook(wb)
}
