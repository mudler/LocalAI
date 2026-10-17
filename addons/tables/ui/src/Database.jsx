// One database: its tables, the typed grid of the selected table, the
// schema controls and sharing for the owner. The role comes from the host's
// listing for this person; what the page offers follows it, and the host
// refuses the rest.
import { describeError } from './lib/errors.js'

export function createDatabase(ctx, ui, api, hooks, parts) {
  const { sdk, h, R, Fragment, t } = ctx
  const { Banner, Problem, RoleBadge, Loading, Link, Button, EmptyState } = ui
  const { databases, tableView, schema, sharing } = parts

  function readTable(search) {
    const m = /(?:^|&)table=([^&]*)/.exec(search || '')
    if (!m) return ''
    try { return decodeURIComponent(m[1]) } catch { return '' }
  }

  function Database({ id }) {
    ctx.useLanguage()
    const [tableName, setTableName] = R.useState(() => readTable(sdk.navigation.current().search))
    const [lost, setLost] = R.useState(null)
    const [notice, setNotice] = R.useState('')
    const revision = R.useRef(null)
    const view = hooks.useLoader(async () => {
      const [listing, schemaOut] = await Promise.all([api.list(), api.inspect(id)])
      const resource = listing.resources.find(r => r.id === id)
      if (!resource) throw Object.assign(new Error('not listed'), { kind: 'denied' })
      revision.current = schemaOut.revision
      return { listing, resource, schema: schemaOut }
    }, [id])

    // Whatever the database says about itself last wins: a reload is never
    // an optimistic guess.
    const data = view.data
    const db = R.useMemo(() => ({
      id,
      get role() { return data ? data.resource.role : 'viewer' },
      revision: () => revision.current,
      setRevision(next) { if (next && next.schema) revision.current = next },
      async change(tool, args, options) {
        const out = await api.change(id, tool, args, revision.current, options)
        if (out && out.revision && out.revision.schema) revision.current = out.revision
        return out
      },
      async refreshRevision() {
        const s = await api.inspect(id)
        revision.current = s.revision
        return s
      },
    }), [id, data])

    R.useEffect(() => {
      if (view.state === 'error' && data === null) {
        const d = describeError(view.error)
        if (d.kind === 'denied' || d.kind === 'withdrawn') setLost(view.error)
      }
    }, [view.state])

    const onLost = (error) => setLost(error || { kind: 'denied' })
    const back = <Link to="databases" testid="tbl-back" className="tbl-link">{t('common.back')}</Link>

    if (lost) {
      return (
        <section className="tbl-page" data-testid="tbl-unavailable">
          <EmptyState icon="lock" title={t('db.unavailable.title')} text={t('db.unavailable.text')}>{back}</EmptyState>
        </section>
      )
    }
    if (!data) {
      if (view.state === 'error') {
        return (
          <section className="tbl-page" data-testid="tbl-page-error">
            <Problem error={view.error} onRetry={view.reload} />
            <div>{back}</div>
          </section>
        )
      }
      return <section className="tbl-page"><Loading /></section>
    }

    const resource = data.resource
    const isOwner = resource.role === 'owner'
    const canWrite = resource.role === 'owner' || resource.role === 'editor'
    const tables = data.schema.detail || []
    const current = tables.find(x => x.name === tableName) || tables[0] || null
    const select = (name) => {
      setTableName(name)
      try { sdk.navigation.navigate(`databases/${id}?table=${encodeURIComponent(name)}`, { replace: true }) } catch { /* the address is a convenience */ }
    }
    const open = (title, render) => sdk.layers.openDialog({ title, render })
    const name = data.schema.name || resource.name || t('common.untitled')
    const changedSchema = (tool, args) => {
      if (tool === 'tables-rename') setTableName(args.newName)
      else if (tool === 'tables-create') setTableName(args.table)
      setNotice(t('schema.done'))
      view.refresh()
    }
    return (
      <section className="tbl-page" data-testid="tbl-database-page" data-resource={id} data-role={resource.role}>
        <header className="tbl-head">
          <div className="tbl-head__main">
            <div>{back}</div>
            <h2 className="tbl-title" data-testid="tbl-db-name">{name}</h2>
            <div className="tbl-db__meta">
              <RoleBadge role={resource.role} />
              {!isOwner && resource.owner ? <span className="tbl-muted">{t('db.sharedBy', { name: resource.owner })}</span> : null}
              {isOwner && resource.sharedWith > 0 ? <span className="tbl-muted">{t('list.shared', { count: resource.sharedWith })}</span> : null}
            </div>
          </div>
          {isOwner ? (
            <div className="tbl-toolbar__group">
              <Button size="sm" icon="users" onClick={() => open(t('share.title', { name }), ({ close }) => (
                <sharing.Sharing resource={{ id, name }} sharing={data.listing.sharing} onChanged={view.refresh} onLost={onLost} close={close} />
              ))}>{t('db.share')}</Button>
              <Button size="sm" icon="edit" onClick={() => open(t('rename.title'), ({ close }) => (
                <databases.NameDialog initial={name} label={t('rename.submit')} close={close} onDone={() => view.refresh()}
                  submit={(n, operationId) => api.rename(id, n, db.revision(), { operationId })} />
              ))}>{t('rename.action')}</Button>
              <Button size="sm" variant="danger" icon="trash" onClick={() => open(t('delete.title'), ({ close }) => (
                <databases.DeleteDialog resource={{ id, name: data.schema.name, revision: db.revision() }} close={close}
                  onDone={() => { try { sdk.navigation.navigate('databases') } catch { /* the page is gone with the database */ } }} />
              ))}>{t('delete.action')}</Button>
            </div>
          ) : null}
        </header>
        {!canWrite ? <Banner tone="info" testid="tbl-readonly">{t('db.readOnlyNote')}</Banner> : null}
        {view.state === 'error' ? <Problem error={view.error} onRetry={view.reload} /> : null}
        {notice ? <Banner tone="success" testid="tbl-notice">{notice}</Banner> : null}
        <nav className="tbl-tables" aria-label={t('db.tables')}>
          <ul className="tbl-tabs" data-testid="tbl-tables">
            {tables.map(x => (
              <li key={x.name}>
                <button type="button" className={current && current.name === x.name ? 'tbl-tab tbl-tab--on' : 'tbl-tab'} aria-current={current && current.name === x.name ? 'true' : undefined} onClick={() => select(x.name)} data-testid="tbl-table-tab">{x.name}</button>
              </li>
            ))}
          </ul>
          {isOwner ? <Button size="sm" icon="plus" onClick={() => open(t('db.newTable'), ({ close }) => <schema.TableNameDialog db={db} onDone={changedSchema} onLost={onLost} close={close} />)}>{t('db.newTable')}</Button> : null}
        </nav>
        {current ? (
          <Fragment>
            <tableView.TableView key={`${id}:${current.name}`} db={db} table={current} canWrite={canWrite} onLost={onLost} onNotice={(what) => setNotice(what && what.count ? t('row.changed', { count: what.count }) : t('row.saved'))} />
            <schema.SchemaPanel db={db} table={current} isOwner={isOwner} onChanged={changedSchema} onLost={onLost} />
          </Fragment>
        ) : (
          <EmptyState icon="table" title={t('db.noTables.title')} text={isOwner ? t('db.noTables.owner') : t('db.noTables.other')} />
        )}
        <p className="tbl-muted tbl-revision" data-testid="tbl-revision">{t('db.revision', { schema: (revision.current || {}).schema || 0, data: (revision.current || {}).data || 0 })}</p>
      </section>
    )
  }

  return { Database }
}
