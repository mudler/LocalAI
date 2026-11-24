import { useState } from 'react'
import { filesApi } from '../../utils/api'
import { Tabs } from '../ui'

const columnName = (i) => {
  let s = ''
  for (let n = i + 1; n > 0; n = Math.floor((n - 1) / 26)) s = String.fromCharCode(65 + ((n - 1) % 26)) + s
  return s
}

// A spreadsheet: a tab per sheet, the first row as a sticky header, row
// numbers, and what was left out.
export default function TableView({ data, item, t }) {
  const sheets = data?.sheets || []
  const [at, setAt] = useState(0)
  const sheet = sheets[Math.min(at, sheets.length - 1)]
  if (!sheet) return <p className="pv__note">{t('preview.sheetEmpty')}</p>
  const width = Math.max(0, ...sheet.rows.map(r => r.length))
  const cols = Array.from({ length: width }, (_, i) => i)
  const [head, ...rest] = sheet.rows
  const more = sheet.total_rows > sheet.rows.length || sheet.total_cols > width
  return (
    <div className="pv-table" data-testid="preview-table">
      {sheets.length > 1 && (
        <Tabs
          label={t('preview.sheets')}
          idPrefix="pv"
          panelId="pv-sheet"
          value={String(Math.min(at, sheets.length - 1))}
          onChange={(id) => setAt(Number(id))}
          items={sheets.map((s, i) => ({ id: String(i), label: s.name, testId: 'preview-sheet-tab' }))}
        />
      )}
      <div
        className="pv-table__scroll" id="pv-sheet" tabIndex={0}
        role={sheets.length > 1 ? 'tabpanel' : 'region'}
        aria-labelledby={sheets.length > 1 ? `pv-tab-${at}` : undefined}
        aria-label={sheets.length > 1 ? undefined : t('preview.sheetLabel', { name: sheet.name })}
      >
        {sheet.rows.length === 0 ? <p className="pv__note">{t('preview.sheetEmpty')}</p> : (
          <table className="pv-grid">
            <thead>
              <tr>
                <th scope="col" className="pv-grid__n" aria-label={t('preview.row')}>1</th>
                {cols.map(c => <th key={c} scope="col" title={columnName(c)}>{head[c] ?? ''}</th>)}
              </tr>
            </thead>
            <tbody>
              {rest.map((r, i) => (
                <tr key={i}>
                  <th scope="row" className="pv-grid__n">{i + 2}</th>
                  {cols.map(c => <td key={c} className={sheet.kinds?.[i + 1]?.[c] === 'n' ? 'pv-grid__num' : undefined}>{r[c] ?? ''}</td>)}
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {more && (
        <p className="pv__note" data-testid="preview-table-more">
          {t('preview.showingRows', { rows: sheet.rows.length, total: sheet.total_rows })}
          {sheet.total_cols > width && ` ${t('preview.showingCols', { cols: width, total: sheet.total_cols })}`}{' '}
          {item.type === 'file' && <a href={filesApi.contentUrl(item.path)} download={item.name}>{t('preview.downloadRest')}</a>}
        </p>
      )}
    </div>
  )
}
