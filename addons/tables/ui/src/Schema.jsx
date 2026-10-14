// The schema controls of a database's owner: tables and columns. Every
// action is one schema tool through the host, under the owner role and the
// revision the owner last read. Destructive ones say what is lost and ask for
// the name to be typed.
import { parseField, emptyState, TYPES } from './lib/codecs.js'
import { describeError } from './lib/errors.js'

export function createSchema(ctx, ui, fields, hooks) {
  const { sdk, h, R, Fragment, t } = ctx
  const { Root, Field, Banner, Problem, Button } = ui
  const { FieldEditor } = fields

  // useChange runs one schema change with the retry rules of every dialog.
  function useChange({ db, onDone, onLost, close }) {
    const [busy, setBusy] = R.useState(false)
    const [error, setError] = R.useState(null)
    const op = hooks.useOperation()
    const last = R.useRef(null)
    const run = async (tool, args) => {
      last.current = { tool, args }
      setBusy(true)
      setError(null)
      try {
        await db.change(tool, args, { operationId: op.current() })
        op.settle()
        onDone && onDone(tool, args)
        close()
      } catch (e) {
        const d = describeError(e)
        if (d.kind !== 'unknown') op.settle()
        if (d.kind === 'denied' || d.kind === 'withdrawn') onLost && onLost(e)
        setError(e)
        setBusy(false)
      }
    }
    return {
      busy,
      error,
      run,
      async reloadAndRetry() {
        setBusy(true)
        try { await db.refreshRevision() } catch (e) { setError(e); setBusy(false); return }
        if (last.current) await run(last.current.tool, last.current.args)
      },
      check() { if (last.current) return run(last.current.tool, last.current.args); return undefined },
    }
  }

  function Failure({ c }) {
    return c.error ? <Problem error={c.error} onReload={c.reloadAndRetry} onCheck={c.check} onRetry={c.check} /> : null
  }

  function TableNameDialog({ db, initial, onDone, onLost, close }) {
    const [name, setName] = R.useState(initial || '')
    const c = useChange({ db, onDone, onLost, close })
    const valid = name.trim() !== '' && name === name.trim() && name.length <= 80
    const submit = (e) => {
      e.preventDefault()
      if (!valid || c.busy) return
      if (initial) c.run('tables-rename', { table: initial, newName: name })
      else c.run('tables-create', { table: name })
    }
    return (
      <Root className="tbl-dialog">
        <form className="tbl-form" onSubmit={submit} data-testid="tbl-table-dialog">
          <Field label={t('schema.tableName')} hint={t('create.hint')} id="tbl-table-name">
            <input id="tbl-table-name" className="tbl-input" type="text" autoFocus autoComplete="off" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Failure c={c} />
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="primary" busy={c.busy} disabled={!valid}>{initial ? t('rename.submit') : t('schema.createTable')}</Button>
          </div>
        </form>
      </Root>
    )
  }

  function AddColumnDialog({ db, table, onDone, onLost, close }) {
    const [name, setName] = R.useState('')
    const [type, setType] = R.useState('text')
    const [required, setRequired] = R.useState(false)
    const [unique, setUnique] = R.useState(false)
    const [withDefault, setWithDefault] = R.useState(false)
    const [def, setDef] = R.useState({ text: '', isNull: false })
    const [defError, setDefError] = R.useState('')
    const c = useChange({ db, onDone, onLost, close })
    const valid = name.trim() !== '' && name === name.trim() && name.length <= 80 && name !== '_id'
    const submit = (e) => {
      e.preventDefault()
      if (!valid || c.busy) return
      const args = { table, column: name, type }
      if (required) args.required = true
      if (unique) args.unique = true
      if (withDefault) {
        const r = parseField({ type, required: false }, def)
        if (!r.ok || r.value === null) { setDefError(r.code || 'required'); return }
        args.default = r.value
      }
      setDefError('')
      c.run('columns-add', args)
    }
    return (
      <Root className="tbl-dialog">
        <form className="tbl-form" onSubmit={submit} data-testid="tbl-column-dialog">
          <Field label={t('schema.columnName')} hint={t('create.hint')} id="tbl-col-name">
            <input id="tbl-col-name" className="tbl-input" type="text" autoFocus autoComplete="off" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t('schema.columnType')} id="tbl-col-type">
            <select id="tbl-col-type" className="tbl-input" value={type} onChange={(e) => { setType(e.target.value); setDef({ text: '', isNull: false }) }}>
              {TYPES.map(x => <option key={x} value={x}>{t(`type.${x}`)}</option>)}
            </select>
          </Field>
          <label className="tbl-check"><input type="checkbox" checked={required} onChange={(e) => setRequired(e.target.checked)} /><span>{t('schema.required')}</span></label>
          <label className="tbl-check"><input type="checkbox" checked={unique} onChange={(e) => setUnique(e.target.checked)} /><span>{t('schema.unique')}</span></label>
          <label className="tbl-check"><input type="checkbox" checked={withDefault} onChange={(e) => setWithDefault(e.target.checked)} /><span>{t('schema.default')}</span></label>
          {withDefault ? <FieldEditor column={{ name: t('schema.default'), type, required: true }} state={def} id="tbl-col-default" error={defError} onChange={setDef} /> : null}
          <Failure c={c} />
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="primary" busy={c.busy} disabled={!valid}>{t('schema.add')}</Button>
          </div>
        </form>
      </Root>
    )
  }

  function ColumnRenameDialog({ db, table, column, onDone, onLost, close }) {
    const [name, setName] = R.useState(column)
    const c = useChange({ db, onDone, onLost, close })
    const valid = name.trim() !== '' && name === name.trim() && name.length <= 80 && name !== column && name !== '_id'
    return (
      <Root className="tbl-dialog">
        <form className="tbl-form" onSubmit={(e) => { e.preventDefault(); if (valid && !c.busy) c.run('columns-rename', { table, column, newName: name }) }} data-testid="tbl-column-rename-dialog">
          <Field label={t('schema.columnName')} id="tbl-col-rename">
            <input id="tbl-col-rename" className="tbl-input" type="text" autoFocus autoComplete="off" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Failure c={c} />
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="primary" busy={c.busy} disabled={!valid}>{t('rename.submit')}</Button>
          </div>
        </form>
      </Root>
    )
  }

  // Dropping a column or a table: what is lost, and the name typed back.
  function DropDialog({ db, tool, args, name, warning, onDone, onLost, close }) {
    const [typed, setTyped] = R.useState('')
    const c = useChange({ db, onDone, onLost, close })
    const match = typed === name
    return (
      <Root className="tbl-dialog">
        <form className="tbl-form" onSubmit={(e) => { e.preventDefault(); if (match && !c.busy) c.run(tool, args) }} data-testid="tbl-drop-dialog">
          <div className="tbl-warning"><p>{warning}</p></div>
          <Field label={t('schema.typeName')} id="tbl-drop-confirm">
            <input id="tbl-drop-confirm" className="tbl-input" type="text" autoFocus autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
          </Field>
          <Failure c={c} />
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="danger" busy={c.busy} disabled={!match}>{t('schema.drop')}</Button>
          </div>
        </form>
      </Root>
    )
  }

  // The panel under the grid.
  function SchemaPanel({ db, table, isOwner, onChanged, onLost }) {
    const open = (title, render) => sdk.layers.openDialog({ title, render })
    const after = (tool, args) => onChanged(tool, args)
    if (!isOwner) {
      return (
        <section className="tbl-schema" data-testid="tbl-schema" aria-label={t('schema.title', { table: table.name })}>
          <h3 className="tbl-subtitle">{t('schema.columns')}</h3>
          <p className="tbl-muted">{t('schema.ownerOnly')}</p>
          <ColumnList table={table} />
        </section>
      )
    }
    return (
      <section className="tbl-schema" data-testid="tbl-schema" aria-label={t('schema.title', { table: table.name })}>
        <div className="tbl-schema__head">
          <h3 className="tbl-subtitle">{t('schema.columns')}</h3>
          <div className="tbl-toolbar__group">
            <Button size="sm" icon="plus" onClick={() => open(t('schema.addColumn'), ({ close }) => <AddColumnDialog db={db} table={table.name} onDone={after} onLost={onLost} close={close} />)}>{t('schema.addColumn')}</Button>
            <Button size="sm" onClick={() => open(t('schema.renameTable'), ({ close }) => <TableNameDialog db={db} initial={table.name} onDone={after} onLost={onLost} close={close} />)}>{t('schema.renameTable')}</Button>
            <Button size="sm" variant="danger" onClick={() => open(t('schema.dropTable'), ({ close }) => <DropDialog db={db} tool="tables-drop" args={{ table: table.name }} name={table.name} warning={t('schema.dropTableWarn', { name: table.name })} onDone={after} onLost={onLost} close={close} />)}>{t('schema.dropTable')}</Button>
          </div>
        </div>
        <ColumnList table={table} actions={(c) => (
          <span className="tbl-toolbar__group">
            <Button size="sm" variant="ghost" aria-label={`${t('schema.rename')} ${c.name}`} onClick={() => open(t('schema.rename'), ({ close }) => <ColumnRenameDialog db={db} table={table.name} column={c.name} onDone={after} onLost={onLost} close={close} />)}>{t('schema.rename')}</Button>
            <Button size="sm" variant="ghost" aria-label={`${t('schema.drop')} ${c.name}`} onClick={() => open(t('schema.drop'), ({ close }) => <DropDialog db={db} tool="columns-drop" args={{ table: table.name, column: c.name }} name={c.name} warning={t('schema.dropColumnWarn', { name: c.name })} onDone={after} onLost={onLost} close={close} />)}>{t('schema.drop')}</Button>
          </span>
        )} />
      </section>
    )
  }

  function ColumnList({ table, actions }) {
    if (table.columns.length === 0) return <p className="tbl-muted">{t('grid.noColumns.title')}</p>
    return (
      <ul className="tbl-columns" data-testid="tbl-columns">
        {table.columns.map(c => (
          <li key={c.name} className="tbl-column" data-column={c.name}>
            <span className="tbl-column__name">{c.name}</span>
            <span className="tbl-muted">
              {t(`type.${c.type}`)}{c.required ? ` · ${t('field.required')}` : ''}{c.unique ? ` · ${t('field.unique')}` : ''}
            </span>
            {actions ? actions(c) : null}
          </li>
        ))}
      </ul>
    )
  }

  return { SchemaPanel, TableNameDialog }
}
