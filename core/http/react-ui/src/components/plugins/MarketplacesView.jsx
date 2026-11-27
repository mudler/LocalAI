/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import ActionMenu from '../ActionMenu'
import ConfirmDialog from '../ConfirmDialog'
import Icon from '../Icon'
import SideSheet from '../SideSheet'
import { BuiltinIcon } from '../addons/AddonBits'
import { Button, Callout, EmptyState, LoadError, SkeletonRows, StatusChip, Switch, Tag, useLoad } from '../ui'
import { relativeTime } from '../../utils/p2p'
import { Sha } from './PluginBits'
import { errorKey, marketplaceChip } from './pluginState'

// The marketplaces: where the plugins come from. Everyone sees them;
// administrators add one, refresh it, turn it off and remove it. The curated
// list LocalAI ships says so.
export default function MarketplacesView({ api, isAdmin, onChanged, addToast }) {
  const { t, i18n } = useTranslation('addons')
  const list = useLoad(() => api.marketplaces.list(), [])
  const [busy, setBusy] = useState({})
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  const [toRemove, setToRemove] = useState(null)
  const [removing, setRemoving] = useState(false)

  const say = (e) => (errorKey(e) ? t(errorKey(e), { defaultValue: e.message }) : e?.message || t('plugins.errors.generic'))
  const run = async (m, fn, okText) => {
    setError('')
    setBusy((b) => ({ ...b, [m.id]: true }))
    try {
      await fn()
      if (okText) addToast?.(okText, 'success')
      await list.reload()
      onChanged?.()
    } catch (e) {
      setError(say(e))
    } finally {
      setBusy((b) => ({ ...b, [m.id]: false }))
    }
  }
  const remove = async () => {
    setRemoving(true)
    await run(toRemove, () => api.marketplaces.remove(toRemove.id), t('plugins.marketplaces.removed', { name: toRemove.name || toRemove.id }))
    setRemoving(false)
    setToRemove(null)
  }

  const add = isAdmin ? <Button variant="primary" icon="plus" onClick={() => setAdding(true)} data-testid="plugins-marketplace-add">{t('plugins.marketplaces.add')}</Button> : null
  return (
    <div className="pl-markets" data-testid="plugins-marketplaces">
      <div className="pl-markets__bar">
        <p className="pl-quiet">{t(isAdmin ? 'plugins.marketplaces.introAdmin' : 'plugins.marketplaces.intro')}</p>
        {add && list.status === 'ready' && list.data.length > 0 && add}
      </div>
      {error && <Callout tone="error" role="alert" testId="plugins-marketplace-error" actions={<Button variant="ghost" size="sm" onClick={() => setError('')}>{t('plugins.dismiss')}</Button>}>{error}</Callout>}
      {list.status === 'loading' && <SkeletonRows count={2} />}
      {list.status === 'error' && <LoadError title={t('plugins.marketplaces.loadFailed')} error={list.error} onRetry={list.retry} />}
      {list.status === 'ready' && list.data.length === 0 && (
        <EmptyState icon="store" title={t('plugins.marketplaces.emptyTitle')} body={t(isAdmin ? 'plugins.marketplaces.emptyAdmin' : 'plugins.marketplaces.emptyUser')} actions={add} testId="plugins-marketplaces-empty" />
      )}
      {list.status === 'ready' && list.data.length > 0 && (
        <ul className="ad-tiles">
          {list.data.map((m) => {
            const chip = marketplaceChip(m)
            const when = relativeTime(m.refreshedAt, i18n.language)
            const items = [
              { key: 'remove', icon: 'trash', label: t('plugins.marketplaces.remove'), danger: true, onClick: () => setToRemove(m) },
            ]
            return (
              <li key={m.id} className="ad-tile pl-tile" data-testid="plugins-marketplace-row" data-id={m.id}>
                <div className="ad-tile__open pl-tile__static">
                  <BuiltinIcon name={m.default ? 'shield' : 'store'} />
                  <span className="ad-tile__text">
                    <span className="ad-tile__head">
                      <span className="ad-tile__title">{m.name || m.id}</span>
                      <StatusChip tone={chip.tone} label={t(`plugins.marketplaces.state.${chip.key}`)} title={m.lastError || undefined} testId="plugins-marketplace-state" />
                      {m.default && <Tag icon="shield" testId="plugins-marketplace-curated">{t('plugins.browse.curated')}</Tag>}
                    </span>
                    <span className="ad-meta">
                      <span className="ad-meta__item pl-mono" title={m.location}><Icon name={m.kind === 'url' ? 'link' : 'git-branch'} aria-hidden="true" /> {m.location}{m.ref ? `@${m.ref}` : ''}</span>
                      {m.headSha && <span className="ad-meta__item"><Sha sha={m.headSha} /></span>}
                      {when && <span className="ad-meta__item" title={m.refreshedAt}><Icon name="clock" aria-hidden="true" /> {t('plugins.marketplaces.refreshedAt', { when })}</span>}
                      {m.lastError && <span className="ad-meta__item pl-error">{t('plugins.marketplaces.lastError', { code: m.lastError })}</span>}
                    </span>
                  </span>
                </div>
                {isAdmin && (
                  <div className="ad-tile__side">
                    <span className="ad-tile__act">
                      <Button size="sm" icon="refresh" busy={busy[m.id]} disabled={busy[m.id] || !m.active} onClick={() => run(m, () => api.marketplaces.refresh(m.id), t('plugins.marketplaces.refreshed', { name: m.name || m.id }))}
                        aria-label={t('plugins.marketplaces.refreshNamed', { name: m.name || m.id })} data-testid="plugins-marketplace-refresh">{t('plugins.marketplaces.refresh')}</Button>
                    </span>
                    <Switch checked={m.enabled} disabled={busy[m.id]} onChange={(on) => run(m, () => api.marketplaces.update(m.id, { enabled: on }))}
                      label={t('plugins.marketplaces.toggle', { name: m.name || m.id })} testId="plugins-marketplace-switch" />
                    <ActionMenu items={items} ariaLabel={t('plugins.marketplaces.menu', { name: m.name || m.id })} compact />
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}
      {adding && <AddMarketplace api={api} onClose={() => setAdding(false)} onAdded={(m) => { setAdding(false); addToast?.(t('plugins.marketplaces.added', { name: m?.name || m?.id || '' }), 'success'); list.reload(); onChanged?.() }} />}
      <ConfirmDialog
        open={Boolean(toRemove)}
        danger
        pending={removing}
        title={toRemove ? t('plugins.marketplaces.removeTitle', { name: toRemove.name || toRemove.id }) : ''}
        message={t('plugins.marketplaces.removeBody')}
        confirmLabel={t('plugins.marketplaces.remove')}
        onConfirm={remove}
        onCancel={() => setToRemove(null)}
      />
    </div>
  )
}

function AddMarketplace({ api, onClose, onAdded }) {
  const { t } = useTranslation('addons')
  const [form, setForm] = useState({ source: '', ref: '', id: '' })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const set = (k) => (e) => setForm((f) => ({ ...f, [k]: e.target.value }))
  const save = async (e) => {
    e?.preventDefault?.()
    if (!form.source.trim()) return
    setSaving(true)
    setError(null)
    try {
      onAdded(await api.marketplaces.add(form))
    } catch (err) {
      setError(err)
    } finally {
      setSaving(false)
    }
  }
  return (
    <SideSheet
      title={t('plugins.marketplaces.addTitle')}
      description={t('plugins.marketplaces.addDescription')}
      onClose={onClose}
      pending={saving}
      closeLabel={t('plugins.review.cancel')}
      testId="plugins-marketplace-sheet"
      labelId="plugins-marketplace-sheet-title"
      footer={(
        <>
          <Button variant="ghost" onClick={onClose} disabled={saving}>{t('plugins.review.cancel')}</Button>
          <Button variant="primary" icon="plus" busy={saving} disabled={saving || !form.source.trim()} onClick={save} data-testid="plugins-marketplace-save">{t('plugins.marketplaces.addConfirm')}</Button>
        </>
      )}
    >
      <form className="pl-form" onSubmit={save} noValidate>
        <div className="pl-field">
          <label htmlFor="pl-mkt-source"><span className="pl-field__label">{t('plugins.marketplaces.source')}</span></label>
          <input id="pl-mkt-source" className="dk-input pl-mono" value={form.source} onChange={set('source')} placeholder="owner/repo" autoComplete="off" spellCheck={false} data-testid="plugins-marketplace-source" />
          <span className="pl-field__help">{t('plugins.marketplaces.sourceHelp')}</span>
        </div>
        <div className="pl-field">
          <label htmlFor="pl-mkt-ref"><span className="pl-field__label">{t('plugins.marketplaces.ref')}</span></label>
          <input id="pl-mkt-ref" className="dk-input pl-mono" value={form.ref} onChange={set('ref')} autoComplete="off" spellCheck={false} />
          <span className="pl-field__help">{t('plugins.marketplaces.refHelp')}</span>
        </div>
        <div className="pl-field">
          <label htmlFor="pl-mkt-id"><span className="pl-field__label">{t('plugins.marketplaces.id')}</span></label>
          <input id="pl-mkt-id" className="dk-input pl-mono" value={form.id} onChange={set('id')} autoComplete="off" spellCheck={false} />
          <span className="pl-field__help">{t('plugins.marketplaces.idHelp')}</span>
        </div>
        {error && <Callout tone="error" role="alert" testId="plugins-marketplace-add-error">{errorKey(error) ? t(errorKey(error), { defaultValue: error.message }) : error.message}</Callout>}
      </form>
    </SideSheet>
  )
}
