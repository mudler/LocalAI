import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import EmptyState from '../ui/EmptyState'
import LoadError from '../ui/LoadError'
import { SkeletonRows } from '../Skeleton'
import EditRemoteSheet from './EditRemoteSheet'
import { remotesApi } from '../../utils/api'
import SkippedList from './SkippedList'
import NoUsable from './NoUsable'
import { ageParts, hostOf, idsText, initialTicked, needsAttention, noUsableModels, resultParts, statusOf, usecasesOf } from '../../utils/remotes'

// New models the person said "not now" to, for as long as this page is open.
// The key holds the ids too, so a model that arrives later shows the banner
// again.
const dismissed = new Set()
const dismissKey = (c) => `${c.id}:${(c.new || []).map(m => m.id).join(',')}`

function useWhen() {
  const { t, i18n } = useTranslation('remotes')
  return (iso) => {
    const parts = ageParts(iso)
    if (parts === undefined) return ''
    if (parts === null) return t('card.justNow')
    try {
      return new Intl.RelativeTimeFormat(i18n.language, { numeric: 'auto' }).format(-parts.value, parts.unit)
    } catch {
      return `${parts.value} ${parts.unit}`
    }
  }
}

// The models a remote lists that no entry uses yet. Ticked ones are added.
function NewModels({ remote, onAdded, addToast }) {
  const { t } = useTranslation('remotes')
  const [ticked, setTicked] = useState(() => initialTicked(remote.kind, remote.new))
  const [busy, setBusy] = useState(false)
  const [, setHidden] = useState(0)
  if (dismissed.has(dismissKey(remote))) return null
  const label = (u) => t(`usecase.${u}`, { defaultValue: u })
  const toggle = (id) => setTicked(l => (l.includes(id) ? l.filter(x => x !== id) : [...l, id]))
  const add = async () => {
    setBusy(true)
    try {
      const models = remote.new.filter(m => ticked.includes(m.id)).map(m => ({ id: m.id, usecases: usecasesOf(remote.kind, m) }))
      const result = await remotesApi.adopt(remote.id, models)
      const { created, skipped, warning } = resultParts(result)
      addToast?.(t('card.adopted', { count: created.length, name: remote.name }), 'success')
      // The models that did not go in stay listed as new, so adding them again
      // is one click. The card keeps the reasons.
      if (skipped.length > 0) addToast?.(`${t('skipped.summary', { count: skipped.length })}: ${idsText(skipped)}`, 'warning')
      onAdded?.(result, { warning, skipped })
    } catch (err) {
      addToast?.(t('card.adoptFailed', { message: err.message }), 'error')
    } finally {
      setBusy(false)
    }
  }
  const notNow = () => { dismissed.add(dismissKey(remote)); setHidden(n => n + 1) }
  return (
    <div className="remotes-new" role="group" aria-label={t('card.newTitle', { count: remote.new.length })} data-testid="remote-new">
      <strong className="remotes-new__title">{t('card.newTitle', { count: remote.new.length })}</strong>
      <ul className="remotes-models">
        {remote.new.map(m => (
          <li key={m.id}>
            <label className="remotes-model">
              <input type="checkbox" className="dk-check" checked={ticked.includes(m.id)} onChange={() => toggle(m.id)} />
              <span className="remotes-model__id">{m.id}</span>
              <span className="remotes-model__uses">{usecasesOf(remote.kind, m).map(u => <span key={u} className="dk-badge">{label(u)}</span>)}</span>
            </label>
          </li>
        ))}
      </ul>
      <div className="remotes-actions">
        <Button size="sm" variant="primary" busy={busy} disabled={ticked.length === 0} onClick={add} data-testid="remote-new-add">{t('card.addSelected')}</Button>
        <Button size="sm" variant="ghost" onClick={notNow} data-testid="remote-new-skip">{t('card.notNow')}</Button>
      </div>
    </div>
  )
}

function Card({ remote, onEdit, onChanged, onViewInstalled, replace, addToast }) {
  const { t } = useTranslation('remotes')
  const when = useWhen()
  const [refreshing, setRefreshing] = useState(false)
  // The skipped models and the warning of the last add, kept on the card.
  const [added, setAdded] = useState(null)
  const status = statusOf(remote)
  const noUsable = noUsableModels(remote)
  const attention = needsAttention(remote)
  const dot = noUsable ? ' dk-dot--warn' : status === 'ok' ? ' dk-dot--ok' : attention ? ' dk-dot--error' : ''
  const statusText = noUsable
    ? t('card.status.noUsable')
    : status === 'ok'
    ? t('card.status.ok', { when: when(remote.last_refresh) || t('card.justNow') })
    : t(`card.status.${status}`)

  const refresh = async () => {
    setRefreshing(true)
    try {
      const fresh = await remotesApi.refresh(remote.id)
      replace(fresh)
      // A refresh that worked can still leave nothing to use.
      if (noUsableModels(fresh)) addToast?.(t('card.refreshWarn', { name: remote.name }), 'warning')
      onChanged?.()
    } catch (err) {
      addToast?.(t('card.refreshFailed', { name: remote.name, message: err.message }), 'error')
    } finally {
      setRefreshing(false)
    }
  }

  const count = remote.entries?.length || 0
  const gone = remote.unavailable?.length || 0
  return (
    <article className="remotes-card" data-testid="remote-card" data-status={status} data-warning={noUsable || undefined} aria-label={remote.name}>
      <header className="remotes-card__head">
        <div className="remotes-card__title">
          <h2 className="remotes-card__name">{remote.name}</h2>
          <span className="dk-badge">{t(`kinds.${remote.kind}`, { defaultValue: remote.kind })}</span>
          <span className="remotes-card__host" title={hostOf(remote.url)}>{hostOf(remote.url)}</span>
        </div>
        <div className="remotes-card__actions">
          <Button size="sm" icon="refresh" busy={refreshing} onClick={refresh} data-testid="remote-refresh">{t('card.refresh')}</Button>
          <Button size="sm" icon="edit" onClick={() => onEdit(remote)} data-testid="remote-edit">{t('card.edit')}</Button>
        </div>
      </header>
      <p className="remotes-card__status" data-testid="remote-status">
        <span className={`dk-dot${dot}`} aria-hidden="true" />
        <span>{statusText}</span>
      </p>

      {attention && (
        <Callout
          tone="error"
          role="alert"
          title={t(status === 'auth_failed' ? 'card.authTitle' : 'card.unreachableTitle')}
          testId="remote-error"
          actions={(
            <Button size="sm" variant="primary" icon={status === 'auth_failed' ? 'key' : 'edit'} onClick={() => onEdit(remote)} data-testid="remote-fix">
              {t(status === 'auth_failed' ? 'card.updateKey' : 'card.editRemote')}
            </Button>
          )}
        >
          {remote.last_error || t(status === 'auth_failed' ? (remote.kind === 'localai' ? 'card.authTextLocalai' : 'card.authText') : 'card.unreachableText')}
        </Callout>
      )}

      {noUsable && <NoUsable remote={remote} onEdit={onEdit} />}

      {added && (added.warning || added.skipped.length > 0) && (
        <div className="remotes-outcome" data-testid="remote-added-note">
          <Callout tone="warn" role="status" testId="remote-warning">{t('result.skippedTitle')}</Callout>
          <SkippedList skipped={added.skipped} />
        </div>
      )}

      {remote.new?.length > 0 && (
        <NewModels key={dismissKey(remote)} remote={remote} addToast={addToast} onAdded={(_result, note) => { setAdded(note); onChanged?.() }} />
      )}

      <footer className="remotes-card__foot">
        <span>
          {t('card.models', { count })}
          {gone > 0 && <span className="remotes-card__gone"> {'·'} {t('card.gone', { count: gone })}</span>}
        </span>
        <Button size="sm" variant="ghost" iconEnd="arrow-right" onClick={() => onViewInstalled(remote.id)} data-testid="remote-view">{t('card.view')}</Button>
      </footer>
    </article>
  )
}

// The Remotes tab of the Models page: one card for each remote.
export default function RemotesView({ state, addToast, onConnect, onChanged, onViewInstalled }) {
  const { t } = useTranslation('remotes')
  const { remotes, loading, error, unsupported, refetch, replace } = state
  // The sheet reads the remote from the list, so a refresh shows in it.
  const [editingId, setEditingId] = useState(null)
  const editing = remotes.find(c => c.id === editingId) || null
  const setEditing = (c) => setEditingId(c ? c.id : null)

  if (unsupported) {
    return <EmptyState title={t('unsupported.title')} body={t('unsupported.body')} testId="remotes-unsupported" />
  }
  if (loading && remotes.length === 0) return <SkeletonRows count={2} />
  if (error && remotes.length === 0) {
    return <LoadError title={t('load.error')} error={{ message: error }} onRetry={refetch} />
  }
  if (remotes.length === 0) {
    return (
      <EmptyState
        title={t('empty.title')}
        body={t('empty.body')}
        actions={<Button variant="primary" icon="plug" onClick={onConnect} data-testid="remotes-empty-connect">{t('sheet.open')}</Button>}
      />
    )
  }

  return (
    <div className="remotes-list" data-testid="remotes-list">
      {remotes.map(c => (
        <Card key={c.id} remote={c} onEdit={setEditing} onChanged={() => { refetch(); onChanged?.() }} onViewInstalled={onViewInstalled} replace={replace} addToast={addToast} />
      ))}
      {editing && (
        <EditRemoteSheet
          remote={editing}
          onClose={() => setEditing(null)}
          onSaved={(updated, { keyChanged, refreshed }) => {
            replace(updated)
            setEditing(null)
            addToast?.(t(keyChanged ? 'edit.savedKey' : 'edit.saved', { name: updated.name }), 'success')
            // The server refreshed after the change: say so when it still fails.
            if (refreshed && (needsAttention(updated) || noUsableModels(updated))) addToast?.(t('edit.savedWarn', { name: updated.name }), 'warning')
            onChanged?.()
          }}
          onRemoved={(c, entries) => {
            setEditing(null)
            addToast?.(t(entries === 'remove' ? 'remove.doneAll' : 'remove.doneKeep', { name: c.name }), 'success')
            refetch()
            onChanged?.()
          }}
        />
      )}
    </div>
  )
}
