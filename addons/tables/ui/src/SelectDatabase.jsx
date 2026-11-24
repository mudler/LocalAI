// The "Select database" chat action: a picker of the databases this person
// may use, and the one the conversation works on now. The selection is kept
// by the host for this person, conversation and add-on; nothing here is a
// global, and the owner authorizes the reference again on every tool call.
import { describeError } from './lib/errors.js'

export function createSelectDatabase(ctx, ui, api, hooks) {
  const { sdk, h, R, Fragment, t } = ctx
  const { Root, Banner, Problem, RoleBadge, Loading, Button, EmptyState } = ui

  function SelectDatabase({ conversationId, close }) {
    ctx.useLanguage()
    const [notice, setNotice] = R.useState('')
    const [error, setError] = R.useState(null)
    const [busy, setBusy] = R.useState('')
    const view = hooks.useLoader(async () => {
      const list = await api.list()
      let selected = null
      try {
        selected = await api.selection.get(conversationId)
      } catch (e) {
        // Nothing selected, or a reference that is not usable any more, is
        // the same answer: the conversation has no database.
        if (describeError(e).kind !== 'not-selected' && !(e && e.kind === 'denied')) throw e
      }
      return { list, selected }
    }, [conversationId])
    const data = view.data
    const choose = async (resource) => {
      setBusy(resource.id)
      setError(null)
      try {
        const out = await api.selection.set(conversationId, resource.id)
        setNotice(t('select.selected', { name: (out && out.name) || resource.name || t('common.untitled') }))
        await view.refresh()
      } catch (e) {
        setError(e)
      } finally {
        setBusy('')
      }
    }
    const clear = async () => {
      setBusy('clear')
      setError(null)
      try {
        await api.selection.clear(conversationId)
        setNotice(t('select.cleared'))
        await view.refresh()
      } catch (e) {
        setError(e)
      } finally {
        setBusy('')
      }
    }
    let body
    if (view.state === 'loading' && !data) body = <Loading />
    else if (!data) body = <Problem error={view.error} onRetry={view.reload} />
    else {
      const selectedId = data.selected ? data.selected.resourceId : ''
      const resources = data.list.resources
      body = (
        <>
          <section aria-label={t('select.current')} data-testid="tbl-selection">
            <h3 className="tbl-subtitle">{t('select.current')}</h3>
            {data.selected
              ? (
                <p data-testid="tbl-selected" data-resource={selectedId}>
                  <strong>{data.selected.name || t('common.untitled')}</strong>{' '}
                  <RoleBadge role={data.selected.role} />
                </p>
              )
              : <p className="tbl-muted" data-testid="tbl-none-selected">{t('select.none')}</p>}
            {data.selected && data.selected.role === 'viewer' ? <p className="tbl-muted">{t('select.viewerNote')}</p> : null}
          </section>
          {resources.length === 0
            ? <EmptyState icon="database" title={t('list.empty.title')} text={t('select.empty')} />
            : (
              <ul className="tbl-dblist" data-testid="tbl-picker">
                {resources.map(r => (
                  <li key={r.id} className="tbl-db" data-testid="tbl-pick" data-resource={r.id} data-role={r.role}>
                    <div className="tbl-db__main">
                      <span className="tbl-db__name">{r.name || t('common.untitled')}</span>
                      <div className="tbl-db__meta">
                        <RoleBadge role={r.role} />
                        {r.role !== 'owner' && r.owner ? <span className="tbl-muted">{t('list.sharedBy', { name: r.owner })}</span> : null}
                      </div>
                    </div>
                    {r.id === selectedId
                      ? <span className="tbl-muted" data-testid="tbl-in-use">{t('select.inUse')}</span>
                      : <Button size="sm" variant="primary" busy={busy === r.id} disabled={busy !== ''} onClick={() => choose(r)} aria-label={`${t('select.use')}: ${r.name || t('common.untitled')}`}>{t('select.use')}</Button>}
                  </li>
                ))}
              </ul>
            )}
        </>
      )
    }
    return (
      <Root className="tbl-dialog tbl-select">
        <div className="tbl-form" data-testid="tbl-select-dialog">
          <p className="tbl-muted">{t('select.intro')}</p>
          {body}
          {notice ? <Banner tone="success" testid="tbl-select-notice">{notice}</Banner> : null}
          {error ? <Problem error={error} /> : null}
          <div className="tbl-actions">
            <Button variant="ghost" disabled={!data || !data.selected || busy !== ''} onClick={clear}>{t('select.clear')}</Button>
            <Button onClick={close}>{t('common.close')}</Button>
          </div>
        </div>
      </Root>
    )
  }

  return { SelectDatabase }
}
