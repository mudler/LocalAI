// One table: the typed grid with structured filter, sort and cursor paging,
// and the bounded row operations for editors and the owner. The grid shows
// what rows-query answered for this person; the buttons are hidden for a
// viewer, and the host refuses a crafted write anyway.
import { buildQuery, newPager, pagerNext, pagerPrev, pagerCursor, flipSort, PAGE_SIZES } from './lib/query.js'
import { setAll, toggle, allSelected } from './lib/batch.js'
import { display } from './lib/codecs.js'
import { describeError } from './lib/errors.js'
import { exportArguments } from './lib/export.js'

const CACHE_LIMIT = 40

export function createTableView(ctx, ui, api, hooks, rows, filters, exporter) {
  const { sdk, h, R, Fragment, t } = ctx
  const { Banner, Problem, Loading, Button, EmptyState } = ui
  const { Filters } = filters

  function remember(key, value) {
    ctx.cache.delete(key)
    ctx.cache.set(key, value)
    while (ctx.cache.size > CACHE_LIMIT) ctx.cache.delete(ctx.cache.keys().next().value)
  }

  function TableView({ db, table, canWrite, onLost, onNotice }) {
    const columns = table.columns
    const [applied, setApplied] = R.useState({ conditions: [], mode: 'and' })
    const [filterOpen, setFilterOpen] = R.useState(false)
    const [filterErrors, setFilterErrors] = R.useState({})
    const [sort, setSort] = R.useState([])
    const [limit, setLimit] = R.useState(50)
    const [pager, setPager] = R.useState(newPager())
    const [selection, setSelection] = R.useState(() => new Set())
    const [bump, setBump] = R.useState(0)
    const [reset, setReset] = R.useState(false)

    // A different table starts from its first page with no filter.
    const tableKey = `${db.id}:${table.name}`
    const lastKey = R.useRef(tableKey)
    R.useEffect(() => {
      if (lastKey.current === tableKey) return
      lastKey.current = tableKey
      setApplied({ conditions: [], mode: 'and' })
      setSort([])
      setPager(newPager())
      setSelection(new Set())
      setFilterOpen(false)
      setFilterErrors({})
    }, [tableKey])

    const query = R.useMemo(() => buildQuery({ table: table.name, conditions: applied.conditions, mode: applied.mode, sort, limit, cursor: pagerCursor(pager) }, columns),
      [table.name, applied, sort, limit, pager, columns])
    const cacheKey = `${tableKey}\u0000${JSON.stringify(query.arguments)}`
    const view = hooks.useLoader(async () => {
      const out = await api.rows(db.id, query.arguments)
      return out
    }, [cacheKey, bump])

    // The answer of the add-on, the page of the cache, or nothing.
    const fresh = view.state === 'ready' && view.data && view.data.table === table.name ? view.data : null
    const shown = fresh || ctx.cache.get(cacheKey) || null
    R.useEffect(() => {
      if (!fresh) return
      remember(cacheKey, fresh)
      db.setRevision(fresh.revision)
      setReset(false)
    }, [fresh, cacheKey])

    // A cursor is valid for one query and one revision. When the add-on
    // refuses it, start again from the first page and say so.
    R.useEffect(() => {
      if (view.state !== 'error') return
      const d = describeError(view.error)
      if (d.kind === 'invalid' && pager.at > 0) { setPager(newPager()); setReset(true) }
      else if (d.kind === 'denied' || d.kind === 'withdrawn') onLost && onLost(view.error)
    }, [view.state, view.error])

    const names = shown ? shown.columns : columns.map(c => c.name)
    const byName = Object.fromEntries(columns.map(c => [c.name, c]))
    const pageRows = shown ? shown.rows : []
    const pageIds = pageRows.map(r => r.id)
    const sorted = (name) => sort.find(s => s.column === name)

    const applyFilter = (conditions, mode) => {
      const built = buildQuery({ table: table.name, conditions, mode, sort, limit }, columns)
      setFilterErrors(built.errors)
      if (Object.keys(built.errors).length > 0) return
      setApplied({ conditions, mode })
      setPager(newPager())
      setSelection(new Set())
    }
    const clearFilter = () => { setApplied({ conditions: [], mode: 'and' }); setFilterErrors({}); setPager(newPager()) }
    const changed = (what) => {
      setSelection(new Set())
      setPager(newPager())
      setBump(n => n + 1)
      onNotice && onNotice(what)
    }
    const open = (title, render) => sdk.layers.openDialog({ title, render })
    const addRows = () => open(t('row.add.title'), ({ close }) => <rows.AddRowsDialog db={db} table={table.name} columns={columns} onDone={changed} onLost={onLost} close={close} />)
    const editRow = (row) => open(t('row.edit.title', { id: row.id }), ({ close }) => <rows.EditRowDialog db={db} table={table.name} columns={columns} row={row} names={names} onDone={changed} onLost={onLost} close={close} />)
    const ids = [...selection]
    const setValue = () => open(t('row.set.title', { count: ids.length }), ({ close }) => <rows.SetValueDialog db={db} table={table.name} columns={columns} ids={ids} onDone={changed} onLost={onLost} close={close} />)
    const removeRows = () => open(t('row.delete.title'), ({ close }) => <rows.DeleteRowsDialog db={db} table={table.name} ids={ids} onDone={changed} onLost={onLost} close={close} />)

    const hasFilter = applied.conditions.length > 0
    // The export is the view the person sees: its filter and its sort. Every
    // role that can read the table may export it.
    const exportFile = () => {
      const built = exportArguments({ table: table.name, conditions: applied.conditions, mode: applied.mode, sort }, columns)
      if (Object.keys(built.errors).length > 0) { setFilterErrors(built.errors); setFilterOpen(true); return }
      open(t('export.title', { table: table.name }), ({ close }) => (
        <exporter.ExportDialog db={db} table={table} args={built.arguments} filtered={hasFilter} close={close} onLost={onLost} />
      ))
    }
    let body
    if (columns.length === 0) {
      body = <EmptyState icon="table" title={t('grid.noColumns.title')} text={db.role === 'owner' ? t('grid.noColumns.owner') : t('grid.noColumns.other')} />
    } else if (!shown && view.state === 'loading') {
      body = <Loading />
    } else if (!shown && view.state === 'error') {
      body = <Problem error={view.error} onRetry={view.reload} onReload={view.reload} />
    } else if (shown && pageRows.length === 0) {
      body = hasFilter
        ? <EmptyState icon="search" title={t('grid.noMatch.title')} text={t('grid.noMatch.text')}><Button onClick={clearFilter}>{t('filter.clear')}</Button></EmptyState>
        : <EmptyState icon="table" title={t('grid.noRows.title')} text={t('grid.noRows.text')}>{canWrite ? <Button variant="primary" icon="plus" onClick={addRows}>{t('grid.addRows')}</Button> : null}</EmptyState>
    } else if (shown) {
      body = (
        <div className="tbl-gridwrap" data-testid="tbl-grid-wrap">
          <table className="tbl-grid" data-testid="tbl-grid" aria-label={t('schema.title', { table: table.name })}>
            <thead>
              <tr>
                {canWrite ? (
                  <th className="tbl-th tbl-th--check" scope="col">
                    <input type="checkbox" aria-label={t('grid.selectAll')} checked={allSelected(selection, pageIds)} onChange={(e) => setSelection(setAll(selection, pageIds, e.target.checked))} />
                  </th>
                ) : null}
                <th className="tbl-th tbl-th--id" scope="col">{t('grid.rowId')}</th>
                {names.map(n => {
                  const s = sorted(n)
                  return (
                    <th key={n} className={`tbl-th tbl-th--${byName[n] ? byName[n].type : 'text'}`} scope="col" aria-sort={s ? (s.desc ? 'descending' : 'ascending') : 'none'}>
                      <button type="button" className="tbl-sort" onClick={() => { setSort(flipSort(sort, n)); setPager(newPager()) }} aria-label={t('grid.sort', { column: n })}>
                        <span>{n}</span>
                        <span className="tbl-sort__mark" aria-hidden="true">{s ? (s.desc ? '▼' : '▲') : ''}</span>
                      </button>
                    </th>
                  )
                })}
                {canWrite ? <th className="tbl-th tbl-th--act" scope="col"><span className="tbl-sr">{t('grid.edit')}</span></th> : null}
              </tr>
            </thead>
            <tbody>
              {pageRows.map(row => (
                <tr key={row.id} className={selection.has(row.id) ? 'tbl-tr tbl-tr--selected' : 'tbl-tr'} data-testid="tbl-row" data-row-id={row.id}>
                  {canWrite ? (
                    <td className="tbl-td tbl-td--check">
                      <input type="checkbox" aria-label={t('grid.select', { id: row.id })} checked={selection.has(row.id)} onChange={() => setSelection(toggle(selection, row.id))} />
                    </td>
                  ) : null}
                  <td className="tbl-td tbl-td--id">{row.id}</td>
                  {names.map((n, i) => {
                    const v = row.values[i]
                    const col = byName[n]
                    const text = display(col, v)
                    return (
                      <td key={n} className={`tbl-td tbl-td--${col ? col.type : 'text'}`} data-column={n}>
                        {v === null || v === undefined ? <span className="tbl-null" aria-label={t('common.empty')}>—</span> : text}
                      </td>
                    )
                  })}
                  {canWrite ? (
                    <td className="tbl-td tbl-td--act">
                      <Button size="sm" variant="ghost" icon="edit" aria-label={t('grid.editRow', { id: row.id })} title={t('grid.edit')} onClick={() => editRow(row)} />
                    </td>
                  ) : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )
    }

    return (
      <div className="tbl-tableview" data-testid="tbl-tableview" data-table={table.name}>
        <div className="tbl-toolbar">
          <div className="tbl-toolbar__group">
            <Button size="sm" icon="search" onClick={() => setFilterOpen(v => !v)} aria-label={t('filter.title')}>
              {hasFilter ? t('filter.active', { count: applied.conditions.length }) : t('filter.title')}
            </Button>
            <Button size="sm" icon="refresh" onClick={() => { setBump(n => n + 1) }}>{t('grid.refresh')}</Button>
            <Button size="sm" icon="download" disabled={columns.length === 0} onClick={exportFile}>{t('export.action')}</Button>
          </div>
          {canWrite ? (
            <div className="tbl-toolbar__group">
              <Button size="sm" variant="primary" icon="plus" disabled={columns.length === 0} onClick={addRows}>{t('grid.addRows')}</Button>
            </div>
          ) : null}
        </div>
        {filterOpen ? <Filters columns={columns} applied={applied.conditions} mode={applied.mode} errors={filterErrors} onApply={applyFilter} onClear={clearFilter} /> : null}
        {canWrite && selection.size > 0 ? (
          <div className="tbl-selbar" role="region" aria-label={t('grid.selected', { count: selection.size })} data-testid="tbl-selbar">
            <span data-testid="tbl-selected-count">{t('grid.selected', { count: selection.size })}</span>
            <Button size="sm" onClick={setValue}>{t('grid.setSelected')}</Button>
            <Button size="sm" variant="danger" onClick={removeRows}>{t('grid.deleteSelected')}</Button>
            <Button size="sm" variant="ghost" onClick={() => setSelection(new Set())}>{t('grid.clearSelection')}</Button>
          </div>
        ) : null}
        {reset ? <Banner tone="info" testid="tbl-cursor-reset">{t('grid.cursorReset')}</Banner> : null}
        {view.state === 'error' && shown ? <Problem error={view.error} onRetry={view.reload} onReload={view.reload} /> : null}
        {view.state === 'loading' && shown ? <p className="tbl-muted" role="status" data-testid="tbl-refreshing">{t('common.refreshing')}</p> : null}
        {body}
        {shown && columns.length > 0 ? (
          <div className="tbl-pager" data-testid="tbl-pager">
            <Button size="sm" disabled={pager.at === 0} onClick={() => setPager(pagerPrev(pager))}>{t('grid.prev')}</Button>
            <span className="tbl-muted" data-testid="tbl-page">{t('grid.page', { page: pager.at + 1 })}</span>
            <Button size="sm" disabled={!shown.hasMore} onClick={() => setPager(pagerNext(pager, shown.cursor))}>{t('grid.next')}</Button>
            <label className="tbl-pagesize">
              <span className="tbl-muted">{t('grid.pageSize')}</span>
              <select className="tbl-input tbl-input--small" value={limit} onChange={(e) => { setLimit(Number(e.target.value)); setPager(newPager()) }}>
                {PAGE_SIZES.map(n => <option key={n} value={n}>{n}</option>)}
              </select>
            </label>
          </div>
        ) : null}
      </div>
    )
  }

  return { TableView }
}
