// The row dialogs: add rows, edit one row, set a value on a selection and
// delete a selection. Every submit is one bounded step per call and sends the
// revision the person last read. A definite refusal never keeps an operation
// identifier; an unknown outcome keeps it, so repeating is the same change.
import { parseField, editorState, emptyState, planUpdate } from './lib/codecs.js'
import { planInsert, insertSteps, idSteps, MAX_INSERT_ROWS, MAX_ROW_IDS } from './lib/batch.js'
import { describeError } from './lib/errors.js'

export function createRows(ctx, ui, fields) {
  const { sdk, h, R, Fragment, t } = ctx
  const { Root, Field, Banner, Problem, Button } = ui
  const { FieldEditor } = fields

  // useSubmit runs a submission and keeps its outcome. `steps` is a list of
  // { tool, arguments } run in order, each with an operation identifier that
  // is kept until the step finished, so a repeat after an unknown outcome or
  // a reload resumes at the step that did not finish.
  function useSubmit({ db, onLost, onDone, close }) {
    const [busy, setBusy] = R.useState(false)
    const [error, setError] = R.useState(null)
    const [progress, setProgress] = R.useState({ done: 0, total: 0 })
    const ops = R.useRef([])
    const finished = R.useRef(0)
    const plan = R.useRef(null)

    const execute = async () => {
      const steps = plan.current
      if (!steps) return
      setBusy(true)
      setError(null)
      setProgress({ done: finished.current, total: steps.length })
      try {
        while (finished.current < steps.length) {
          const at = finished.current
          if (!ops.current[at]) ops.current[at] = `op-${(globalThis.crypto && globalThis.crypto.randomUUID) ? globalThis.crypto.randomUUID() : Math.random().toString(16).slice(2)}`
          await db.change(steps[at].tool, steps[at].arguments, { operationId: ops.current[at] })
          finished.current = at + 1
          setProgress({ done: finished.current, total: steps.length })
        }
        plan.current = null
        setBusy(false)
        onDone(finished.current)
        close()
      } catch (e) {
        const d = describeError(e)
        // A definite refusal frees the step's identifier; an unknown outcome
        // keeps it so the repeat is the same operation.
        if (d.kind !== 'unknown') ops.current[finished.current] = ''
        if (d.kind === 'denied' || d.kind === 'withdrawn') onLost && onLost(e)
        setError(e)
        setBusy(false)
      }
    }
    return {
      busy,
      error,
      progress,
      start(steps) { plan.current = steps; ops.current = []; finished.current = 0; return execute() },
      // Reload the revision, then run the unfinished steps again.
      async reloadAndRetry() {
        setBusy(true)
        try { await db.refreshRevision() } catch (e) { setError(e); setBusy(false); return }
        setBusy(false)
        return execute()
      },
      check() { return execute() },
      partial: () => (plan.current && finished.current > 0 ? finished.current : 0),
    }
  }

  function Errors({ s, steps, rowsPerStep }) {
    if (!s.error) return null
    const partial = s.partial()
    return (
      <>
        <Problem error={s.error} onReload={s.reloadAndRetry} onCheck={s.check} onRetry={s.check} />
        {partial > 0 ? <Banner tone="warn">{t('row.partial', { done: Math.min(partial * rowsPerStep, steps * rowsPerStep), total: s.progress.total * rowsPerStep })}</Banner> : null}
      </>
    )
  }

  // ---- add rows ----------------------------------------------------------

  function AddRowsDialog({ db, table, columns, onDone, onLost, close }) {
    const blank = () => Object.fromEntries(columns.map(c => [c.name, emptyState(c)]))
    const [rows, setRows] = R.useState(() => [blank()])
    const [errors, setErrors] = R.useState({})
    const s = useSubmit({ db, onLost, onDone: (n) => onDone({ kind: 'insert', count: rows.length, steps: n }), close })
    R.useEffect(() => { sdk.markDirty(true); return () => sdk.markDirty(false) }, [])
    const submit = (e) => {
      e.preventDefault()
      if (s.busy) return
      const plan = planInsert(rows, columns)
      setErrors(plan.errors)
      if (Object.keys(plan.errors).length > 0) return
      s.start(insertSteps(plan.rows).map(chunk => ({ tool: 'rows-insert', arguments: { table, rows: chunk } })))
    }
    const set = (i, name, state) => setRows(list => list.map((r, at) => (at === i ? { ...r, [name]: state } : r)))
    return (
      <Root className="tbl-dialog">
        <form className="tbl-form" onSubmit={submit} data-testid="tbl-add-dialog">
          {rows.map((row, i) => (
            <fieldset className="tbl-rowform" key={i} data-testid="tbl-add-row">
              <legend>{t('row.add.n', { n: i + 1 })}</legend>
              {columns.map((c, ci) => (
                <FieldEditor key={c.name} column={c} state={row[c.name]} id={`tbl-add-${i}-${ci}`} autoFocus={i === rows.length - 1 && ci === 0 && rows.length > 1}
                  error={errors[`${i}:${c.name}`]} onChange={(st) => set(i, c.name, st)} />
              ))}
              {rows.length > 1 ? <Button size="sm" variant="ghost" onClick={() => setRows(list => list.filter((_, at) => at !== i))}>{t('row.add.remove', { n: i + 1 })}</Button> : null}
            </fieldset>
          ))}
          <div className="tbl-actions tbl-actions--start">
            <Button size="sm" icon="plus" disabled={rows.length >= MAX_INSERT_ROWS} onClick={() => setRows(list => [...list, blank()])}>{t('row.add.another')}</Button>
            {rows.length >= MAX_INSERT_ROWS ? <span className="tbl-muted">{t('row.add.max', { count: MAX_INSERT_ROWS })}</span> : null}
          </div>
          <Errors s={s} steps={1} rowsPerStep={rows.length} />
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="primary" busy={s.busy}>{rows.length === 1 ? t('row.add.submit1') : t('row.add.submit', { count: rows.length })}</Button>
          </div>
        </form>
      </Root>
    )
  }

  // ---- edit one row ------------------------------------------------------

  function EditRowDialog({ db, table, columns, row, names, onDone, onLost, close }) {
    const original = {}
    names.forEach((n, i) => { original[n] = row.values[i] })
    const [state, setState] = R.useState(() => Object.fromEntries(columns.map(c => [c.name, editorState(c, original[c.name])])))
    const [errors, setErrors] = R.useState({})
    const [nochange, setNochange] = R.useState(false)
    const s = useSubmit({ db, onLost, onDone: () => onDone({ kind: 'update', count: 1 }), close })
    R.useEffect(() => { sdk.markDirty(true); return () => sdk.markDirty(false) }, [])
    const submit = (e) => {
      e.preventDefault()
      if (s.busy) return
      const { set, errors: errs } = planUpdate(columns, original, state)
      setErrors(errs)
      if (Object.keys(errs).length > 0) return
      if (Object.keys(set).length === 0) { setNochange(true); return }
      setNochange(false)
      s.start([{ tool: 'rows-update', arguments: { table, ids: [row.id], set } }])
    }
    return (
      <Root className="tbl-dialog">
        <form className="tbl-form" onSubmit={submit} data-testid="tbl-edit-dialog">
          {columns.map((c, i) => (
            <FieldEditor key={c.name} column={c} state={state[c.name]} id={`tbl-edit-${i}`} autoFocus={i === 0} error={errors[c.name]}
              onChange={(st) => setState(m => ({ ...m, [c.name]: st }))} />
          ))}
          {nochange ? <Banner tone="info">{t('row.edit.nochange')}</Banner> : null}
          <Errors s={s} steps={1} rowsPerStep={1} />
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="primary" busy={s.busy}>{t('common.save')}</Button>
          </div>
        </form>
      </Root>
    )
  }

  // ---- set a value on a selection ---------------------------------------

  function SetValueDialog({ db, table, columns, ids, onDone, onLost, close }) {
    const [column, setColumn] = R.useState(columns[0].name)
    const col = columns.find(c => c.name === column) || columns[0]
    const [state, setState] = R.useState(() => emptyState(columns[0]))
    const [error, setError] = R.useState('')
    const s = useSubmit({ db, onLost, onDone: () => onDone({ kind: 'update', count: ids.length }), close })
    const steps = idSteps(ids)
    const submit = (e) => {
      e.preventDefault()
      if (s.busy) return
      const r = parseField(col, state)
      if (!r.ok) { setError(r.code); return }
      setError('')
      s.start(steps.map(chunk => ({ tool: 'rows-update', arguments: { table, ids: chunk, set: { [col.name]: r.value } } })))
    }
    return (
      <Root className="tbl-dialog">
        <form className="tbl-form" onSubmit={submit} data-testid="tbl-set-dialog">
          <Field label={t('row.set.column')} id="tbl-set-column">
            <select id="tbl-set-column" className="tbl-input" value={column} onChange={(e) => { const c = columns.find(x => x.name === e.target.value); setColumn(c.name); setState(emptyState(c)); setError('') }}>
              {columns.map(c => <option key={c.name} value={c.name}>{c.name}</option>)}
            </select>
          </Field>
          <FieldEditor column={{ ...col, name: t('row.set.value') }} state={state} id="tbl-set-value" error={error} onChange={setState} />
          {steps.length > 1 ? <p className="tbl-muted">{t('row.steps', { count: MAX_ROW_IDS })}</p> : null}
          {s.busy && s.progress.total > 1 ? <p className="tbl-muted" role="status">{t('row.progress', { done: s.progress.done, total: s.progress.total })}</p> : null}
          <Errors s={s} steps={steps.length} rowsPerStep={MAX_ROW_IDS} />
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="primary" busy={s.busy}>{t('row.set.submit', { count: ids.length })}</Button>
          </div>
        </form>
      </Root>
    )
  }

  // ---- delete a selection -----------------------------------------------

  function DeleteRowsDialog({ db, table, ids, onDone, onLost, close }) {
    const s = useSubmit({ db, onLost, onDone: () => onDone({ kind: 'delete', count: ids.length }), close })
    const steps = idSteps(ids)
    const submit = (e) => {
      e.preventDefault()
      if (s.busy) return
      s.start(steps.map(chunk => ({ tool: 'rows-delete', arguments: { table, ids: chunk } })))
    }
    return (
      <Root className="tbl-dialog">
        <form className="tbl-form" onSubmit={submit} data-testid="tbl-delete-rows-dialog">
          <div className="tbl-warning"><p>{t('row.delete.text', { count: ids.length })}</p></div>
          {steps.length > 1 ? <p className="tbl-muted">{t('row.steps', { count: MAX_ROW_IDS })}</p> : null}
          {s.busy && s.progress.total > 1 ? <p className="tbl-muted" role="status">{t('row.progress', { done: s.progress.done, total: s.progress.total })}</p> : null}
          <Errors s={s} steps={steps.length} rowsPerStep={MAX_ROW_IDS} />
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="danger" busy={s.busy}>{t('row.delete.submit', { count: ids.length })}</Button>
          </div>
        </form>
      </Root>
    )
  }

  return { AddRowsDialog, EditRowDialog, SetValueDialog, DeleteRowsDialog, useSubmit }
}
