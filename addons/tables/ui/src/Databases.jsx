// The list of databases: the ones the person owns and the ones shared with
// them, with create, rename and delete for the owner. Everything shown is
// what the host's listing answered for this person; the host decides every
// action again.
import { describeError } from './lib/errors.js'

export function createDatabases(ctx, ui, api, hooks) {
  const { sdk, h, R, Fragment, t } = ctx
  const { Root, Field, Banner, Problem, RoleBadge, Loading, Link, Button, EmptyState } = ui

  // ---- dialogs -----------------------------------------------------------

  function NameDialog({ title, initial, submit, label, onDone, close }) {
    const [name, setName] = R.useState(initial || '')
    const [busy, setBusy] = R.useState(false)
    const [error, setError] = R.useState(null)
    const op = hooks.useOperation()
    const trimmed = name.trim()
    const valid = trimmed.length >= 1 && name.length <= 80 && name === trimmed
    const send = async (e) => {
      e.preventDefault()
      if (!valid || busy) return
      setBusy(true)
      setError(null)
      try {
        const out = await submit(trimmed, op.current())
        op.settle()
        onDone(out)
        close()
      } catch (err) {
        if (describeError(err).kind !== 'unknown') op.settle()
        setError(err)
        setBusy(false)
      }
    }
    return (
      <Root className="tbl-dialog">
        <form onSubmit={send} className="tbl-form" data-testid="tbl-name-dialog">
          <Field label={t('create.name')} hint={t('create.hint')} id="tbl-name">
            <input id="tbl-name" className="tbl-input" type="text" autoFocus autoComplete="off" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          {error ? <Problem error={error} /> : null}
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="primary" busy={busy} disabled={!valid}>{label}</Button>
          </div>
        </form>
      </Root>
    )
  }

  function DeleteDialog({ resource, onDone, close }) {
    const [typed, setTyped] = R.useState('')
    const [busy, setBusy] = R.useState(false)
    const [error, setError] = R.useState(null)
    const [pending, setPending] = R.useState(false)
    const op = hooks.useOperation()
    const name = resource.name || ''
    const match = name !== '' && typed === name
    const send = async (e) => {
      e.preventDefault()
      if (!match || busy) return
      setBusy(true)
      setError(null)
      try {
        await api.remove(resource.id, typed, resource.revision, { operationId: op.current() })
        op.settle()
        onDone()
        close()
      } catch (err) {
        const d = describeError(err)
        if (d.kind === 'unknown') setPending(true)
        else op.settle()
        setError(err)
        setBusy(false)
      }
    }
    return (
      <Root className="tbl-dialog">
        <form onSubmit={send} className="tbl-form" data-testid="tbl-delete-dialog">
          {name === '' ? <Banner tone="warn">{t('delete.noName')}</Banner> : (
            <div className="tbl-warning" data-testid="tbl-delete-warnings">
              <p>{t('delete.warn1', { name })}</p>
              <p>{t('delete.warn2')}</p>
              <p>{t('delete.warn3')}</p>
            </div>
          )}
          <Field label={t('delete.type')} id="tbl-confirm">
            <input id="tbl-confirm" className="tbl-input" type="text" autoFocus autoComplete="off" value={typed} disabled={name === ''} onChange={(e) => setTyped(e.target.value)} />
          </Field>
          {pending ? <Banner tone="warn">{t('delete.pending')}</Banner> : null}
          {error ? <Problem error={error} /> : null}
          <div className="tbl-actions">
            <Button onClick={close}>{t('common.cancel')}</Button>
            <Button type="submit" variant="danger" busy={busy} disabled={!match}>{pending ? t('error.checkAgain') : t('delete.submit')}</Button>
          </div>
        </form>
      </Root>
    )
  }

  // ---- the page ----------------------------------------------------------

  function Row({ resource, onChanged }) {
    const owner = resource.role === 'owner'
    const open = (kind) => {
      if (kind === 'rename') {
        sdk.layers.openDialog({
          title: t('rename.title'),
          render: ({ close }) => <NameDialog title={t('rename.title')} initial={resource.name} label={t('rename.submit')} close={close} onDone={onChanged}
            submit={(name, operationId) => api.rename(resource.id, name, resource.revision, { operationId })} />,
        })
      } else {
        sdk.layers.openDialog({
          title: t('delete.title'),
          render: ({ close }) => <DeleteDialog resource={resource} close={close} onDone={onChanged} />,
        })
      }
    }
    const shared = owner
      ? (resource.sharedWith > 0 ? t('list.shared', { count: resource.sharedWith }) : t('list.private'))
      : (resource.owner ? t('list.sharedBy', { name: resource.owner }) : '')
    return (
      <li className="tbl-db" data-testid="tbl-database" data-resource={resource.id} data-role={resource.role}>
        <div className="tbl-db__main">
          <Link to={`databases/${resource.id}`} className="tbl-db__name" testid="tbl-database-open">{resource.name || t('common.untitled')}</Link>
          <div className="tbl-db__meta">
            <RoleBadge role={resource.role} />
            <span className="tbl-muted" data-testid="tbl-database-shared">{shared}</span>
          </div>
        </div>
        {owner ? (
          <div className="tbl-db__actions">
            <Button size="sm" variant="ghost" onClick={() => open('rename')} aria-label={`${t('rename.action')} ${resource.name || ''}`}>{t('rename.action')}</Button>
            <Button size="sm" variant="ghost" onClick={() => open('delete')} aria-label={`${t('delete.action')} ${resource.name || ''}`}>{t('delete.action')}</Button>
          </div>
        ) : null}
      </li>
    )
  }

  function Databases() {
    ctx.useLanguage()
    const view = hooks.useLoader(() => api.list(), [])
    const data = view.data
    const canCreate = Boolean(data && Array.isArray(data.operations) && data.operations.includes('create'))
    const create = () => sdk.layers.openDialog({
      title: t('create.title'),
      description: t('create.private'),
      render: ({ close }) => <NameDialog title={t('create.title')} label={t('create.submit')} close={close}
        onDone={(out) => { view.refresh(); if (out && out.id) { try { sdk.navigation.navigate(`databases/${out.id}`) } catch { /* stays on the list */ } } }}
        submit={(name, operationId) => api.create(name, { operationId })} />,
    })
    let body
    if (view.state === 'loading' && !data) body = <Loading />
    else if (view.state === 'error' && !data) {
      const d = describeError(view.error)
      body = d.kind === 'denied'
        ? <EmptyState icon="lock" title={t('list.denied.title')} text={t('list.denied.text')} />
        : <EmptyState icon="database" title={t('list.unavailable.title')} text={t('list.unavailable.text')}><Button onClick={view.reload}>{t('common.retry')}</Button></EmptyState>
    } else if (data && data.resources.length === 0) {
      body = (
        <EmptyState icon="database" title={t('list.empty.title')} text={t('list.empty.text')}>
          {canCreate ? <Button variant="primary" icon="plus" onClick={create}>{t('list.new')}</Button> : null}
        </EmptyState>
      )
    } else if (data) {
      body = (
        <>
          {view.state === 'error' ? <Problem error={view.error} onRetry={view.reload} /> : null}
          <ul className="tbl-dblist" data-testid="tbl-databases">
            {data.resources.map(r => <Row key={r.id} resource={r} onChanged={view.refresh} />)}
          </ul>
          {data.truncated ? <p className="tbl-muted">{t('list.truncated')}</p> : null}
        </>
      )
    }
    return (
      <section className="tbl-page" data-testid="tbl-list-page" aria-labelledby="tbl-list-title">
        <header className="tbl-head">
          <div>
            <h2 id="tbl-list-title" className="tbl-title">{t('list.heading')}</h2>
            <p className="tbl-muted">{t('list.intro')}</p>
          </div>
          {canCreate && data && data.resources.length > 0 ? <Button variant="primary" icon="plus" onClick={create}>{t('list.new')}</Button> : null}
        </header>
        {body}
      </section>
    )
  }

  return { Databases, NameDialog, DeleteDialog }
}
