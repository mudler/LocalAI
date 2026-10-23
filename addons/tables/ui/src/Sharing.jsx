// Sharing of a whole database, for its owner: who has access, and a picker
// that finds named accounts and returns only an immutable ID and a name.
// Where the installation has no named accounts the panel says so and calls
// nothing.
import { describeError } from './lib/errors.js'

export function createSharing(ctx, ui, api, hooks) {
  const { sdk, h, R, t } = ctx
  const { Root, Field, Banner, Problem, Loading, Button, EmptyState } = ui

  function Sharing({ resource, sharing, onChanged, onLost, close }) {
    const [query, setQuery] = R.useState('')
    const debounced = hooks.useDebounced(query.trim(), 250)
    const [notice, setNotice] = R.useState('')
    const [actionError, setActionError] = R.useState(null)
    const [busy, setBusy] = R.useState('')
    const [confirming, setConfirming] = R.useState('')
    const grants = hooks.useLoader(() => (sharing ? api.grants(resource.id) : Promise.resolve(null)), [resource.id, sharing])
    const found = hooks.useLoader(() => (sharing && debounced.length >= 2 ? api.searchUsers(resource.id, debounced) : Promise.resolve(null)), [resource.id, debounced, sharing])

    if (!sharing) {
      return (
        <Root className="tbl-dialog">
          <div data-testid="tbl-sharing-disabled">
            <EmptyState icon="users" title={t('share.disabled.title')} text={t('share.disabled.text')} />
          </div>
        </Root>
      )
    }
    const list = grants.data ? grants.data.grants : []
    const have = new Set(list.map(g => g.userId))
    const act = async (key, fn, message) => {
      setBusy(key)
      setActionError(null)
      setNotice('')
      try {
        await fn()
        setNotice(message)
        await grants.refresh()
        onChanged && onChanged()
      } catch (e) {
        const d = describeError(e)
        if (d.kind === 'denied' || d.kind === 'withdrawn') onLost && onLost(e)
        setActionError(e)
      } finally {
        setBusy('')
        setConfirming('')
      }
    }
    const nameOf = (g) => g.displayName || t('share.unnamed')
    return (
      <Root className="tbl-dialog tbl-sharing">
        <div className="tbl-form" data-testid="tbl-sharing">
          <p className="tbl-muted">{t('share.intro')}</p>
          <h3 className="tbl-subtitle">{t('share.people')}</h3>
          {grants.state === 'loading' && !grants.data ? <Loading /> : null}
          {grants.state === 'error' ? <Problem error={grants.error} onRetry={grants.reload} /> : null}
          {grants.state !== 'loading' && list.length === 0 && grants.state !== 'error' ? <p className="tbl-muted" data-testid="tbl-no-grants">{t('share.none')}</p> : null}
          <ul className="tbl-grants" data-testid="tbl-grants">
            {list.map(g => (
              <li key={g.userId} className="tbl-grant" data-user={g.userId} data-role={g.role} data-testid="tbl-grant">
                <span className="tbl-grant__name">{nameOf(g)}</span>
                <select className="tbl-input tbl-input--small" aria-label={t('share.role', { name: nameOf(g) })} value={g.role} disabled={busy !== ''}
                  onChange={(e) => act(`role:${g.userId}`, () => api.setGrant(resource.id, g.userId, e.target.value), t('share.granted', { name: nameOf(g) }))}>
                  <option value="viewer">{t('role.viewer')}</option>
                  <option value="editor">{t('role.editor')}</option>
                </select>
                {confirming === g.userId ? (
                  <span className="tbl-toolbar__group">
                    <span className="tbl-muted">{t('share.revokeConfirm', { name: nameOf(g) })}</span>
                    <Button size="sm" variant="danger" busy={busy === `revoke:${g.userId}`} onClick={() => act(`revoke:${g.userId}`, () => api.revokeGrant(resource.id, g.userId), t('share.revoked', { name: nameOf(g) }))}>{t('share.revoke')}</Button>
                    <Button size="sm" variant="ghost" onClick={() => setConfirming('')}>{t('common.cancel')}</Button>
                  </span>
                ) : <Button size="sm" variant="ghost" disabled={busy !== ''} onClick={() => setConfirming(g.userId)} aria-label={`${t('share.revoke')} ${nameOf(g)}`}>{t('share.revoke')}</Button>}
              </li>
            ))}
          </ul>
          <Field label={t('share.search')} hint={t('share.searchHint')} id="tbl-share-search">
            <input id="tbl-share-search" className="tbl-input" type="text" autoComplete="off" autoFocus value={query} onChange={(e) => setQuery(e.target.value)} />
          </Field>
          {found.state === 'error' ? <Problem error={found.error} onRetry={found.reload} /> : null}
          {found.data ? (() => {
            const fresh = found.data.users.filter(u => !have.has(u.id))
            if (fresh.length === 0) {
              return <p className="tbl-muted" data-testid="tbl-no-users">{found.data.users.length === 0 ? t('share.noResults') : t('share.alreadyShared')}</p>
            }
            return (
              <ul className="tbl-grants" data-testid="tbl-users">
                {fresh.map(u => (
                  <li key={u.id} className="tbl-grant" data-user={u.id} data-testid="tbl-user">
                    <span className="tbl-grant__name">{u.displayName || t('share.unnamed')}</span>
                    <span className="tbl-toolbar__group">
                      <Button size="sm" busy={busy === `add:${u.id}:viewer`} disabled={busy !== ''} onClick={() => act(`add:${u.id}:viewer`, () => api.setGrant(resource.id, u.id, 'viewer'), t('share.granted', { name: u.displayName }))}>{t('share.addViewer')}</Button>
                      <Button size="sm" busy={busy === `add:${u.id}:editor`} disabled={busy !== ''} onClick={() => act(`add:${u.id}:editor`, () => api.setGrant(resource.id, u.id, 'editor'), t('share.granted', { name: u.displayName }))}>{t('share.addEditor')}</Button>
                    </span>
                  </li>
                ))}
              </ul>
            )
          })() : null}
          {notice ? <Banner tone="success" testid="tbl-share-notice">{notice}</Banner> : null}
          {actionError ? <Problem error={actionError} /> : null}
          <div className="tbl-actions"><Button onClick={close}>{t('common.close')}</Button></div>
        </div>
      </Root>
    )
  }

  return { Sharing }
}
