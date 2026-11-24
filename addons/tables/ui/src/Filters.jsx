// The structured filter editor of the row grid: a list of conditions over the
// table's own columns, joined by all or any. It builds the filter tree
// rows-query takes (lib/filters.js) and never anything free-form.
import { operators, needsValue, blankCondition, columnType, ID_COLUMN, MAX_NODES } from './lib/filters.js'

export function createFilters(ctx, ui) {
  const { h, R, t } = ctx
  const { Button } = ui

  function Filters({ columns, applied, mode, errors, onApply, onClear }) {
    const [draft, setDraft] = R.useState(() => (applied.length > 0 ? applied : []))
    const [draftMode, setDraftMode] = R.useState(mode)
    const names = [ID_COLUMN, ...columns.map(c => c.name)]
    const update = (i, patch) => setDraft(list => list.map((c, at) => {
      if (at !== i) return c
      const next = { ...c, ...patch }
      if (patch.column !== undefined) {
        const ops = operators(columnType(columns, next.column))
        if (!ops.includes(next.op)) next.op = ops[0] || 'eq'
      }
      return next
    }))
    const add = () => setDraft(list => (list.length >= MAX_NODES ? list : [...list, { ...blankCondition(columns) }]))
    const submit = (e) => { e.preventDefault(); onApply(draft, draftMode) }
    return (
      <form className="tbl-filters" onSubmit={submit} data-testid="tbl-filters" aria-label={t('filter.title')}>
        {draft.length > 1 ? (
          <div className="tbl-filters__mode">
            <label className="tbl-check"><input type="radio" name="tbl-mode" checked={draftMode === 'and'} onChange={() => setDraftMode('and')} /><span>{t('filter.all')}</span></label>
            <label className="tbl-check"><input type="radio" name="tbl-mode" checked={draftMode === 'or'} onChange={() => setDraftMode('or')} /><span>{t('filter.any')}</span></label>
          </div>
        ) : null}
        {draft.map((c, i) => {
          const type = columnType(columns, c.column)
          const ops = operators(type)
          const error = errors && errors[i]
          const valueId = `tbl-f-${i}-value`
          return (
            <div className={error ? 'tbl-cond tbl-cond--error' : 'tbl-cond'} key={i} data-testid="tbl-condition">
              <select className="tbl-input" aria-label={t('filter.column')} value={c.column} onChange={(e) => update(i, { column: e.target.value })}>
                {names.map(n => <option key={n} value={n}>{n === ID_COLUMN ? t('grid.rowId') : n}</option>)}
              </select>
              <select className="tbl-input" aria-label={t('filter.operator')} value={c.op} onChange={(e) => update(i, { op: e.target.value })}>
                {ops.map(o => <option key={o} value={o}>{t(`op.${o}`)}</option>)}
              </select>
              {needsValue(c.op) ? (
                type === 'boolean'
                  ? (
                    <select id={valueId} className="tbl-input" aria-label={t('filter.value')} value={c.value === 'false' ? 'false' : 'true'} onChange={(e) => update(i, { value: e.target.value })}>
                      <option value="true">{t('common.yes')}</option>
                      <option value="false">{t('common.no')}</option>
                    </select>
                  )
                  : <input id={valueId} className="tbl-input" type="text" aria-label={c.op === 'in' ? t('filter.values') : t('filter.value')} placeholder={c.op === 'in' ? t('filter.values') : ''} value={c.value} autoComplete="off" onChange={(e) => update(i, { value: e.target.value })} />
              ) : <span className="tbl-cond__none" />}
              <Button size="sm" variant="ghost" icon="close" aria-label={t('filter.remove')} title={t('filter.remove')} onClick={() => setDraft(list => list.filter((_, at) => at !== i))} />
              {error ? <p className="tbl-error tbl-cond__error" role="alert">{t(`filter.error.${error}`)}</p> : null}
            </div>
          )
        })}
        <div className="tbl-actions tbl-actions--start">
          <Button size="sm" icon="plus" onClick={add}>{t('filter.add')}</Button>
          <Button size="sm" variant="primary" type="submit">{t('filter.apply')}</Button>
          <Button size="sm" onClick={() => { setDraft([]); onClear() }}>{t('filter.clear')}</Button>
        </div>
      </form>
    )
  }

  return { Filters }
}
