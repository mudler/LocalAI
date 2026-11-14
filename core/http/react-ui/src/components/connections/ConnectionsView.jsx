import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import EmptyState from '../ui/EmptyState'
import LoadError from '../ui/LoadError'
import { SkeletonRows } from '../Skeleton'
import EditConnectionSheet from './EditConnectionSheet'
import { connectionsApi } from '../../utils/api'
import { ageParts, hostOf, initialTicked, needsAttention, statusOf } from '../../utils/connections'

// New models the person said "not now" to, for as long as this page is open.
// The key holds the ids too, so a model that arrives later shows the banner
// again.
const dismissed = new Set()
const dismissKey = (c) => `${c.id}:${(c.new || []).map(m => m.id).join(',')}`

function useWhen() {
  const { t, i18n } = useTranslation('connections')
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
function NewModels({ connection, onAdded, addToast }) {
  const { t } = useTranslation('connections')
  const [ticked, setTicked] = useState(() => initialTicked(connection.kind, connection.new))
  const [busy, setBusy] = useState(false)
  const [, setHidden] = useState(0)
  if (dismissed.has(dismissKey(connection))) return null
  const label = (u) => t(`usecase.${u}`, { defaultValue: u })
  const toggle = (id) => setTicked(l => (l.includes(id) ? l.filter(x => x !== id) : [...l, id]))
  const add = async () => {
    setBusy(true)
    try {
      const models = connection.new.filter(m => ticked.includes(m.id)).map(m => ({ id: m.id, usecases: m.usecases || [] }))
      const result = await connectionsApi.adopt(connection.id, models)
      addToast?.(t('card.adopted', { count: result?.created?.length ?? models.length, name: connection.name }), 'success')
      onAdded?.(result)
    } catch (err) {
      addToast?.(t('card.adoptFailed', { message: err.message }), 'error')
    } finally {
      setBusy(false)
    }
  }
  const notNow = () => { dismissed.add(dismissKey(connection)); setHidden(n => n + 1) }
  return (
    <div className="conn-new" role="group" aria-label={t('card.newTitle', { count: connection.new.length })} data-testid="connection-new">
      <strong className="conn-new__title">{t('card.newTitle', { count: connection.new.length })}</strong>
      <ul className="conn-models">
        {connection.new.map(m => (
          <li key={m.id}>
            <label className="conn-model">
              <input type="checkbox" className="dk-check" checked={ticked.includes(m.id)} onChange={() => toggle(m.id)} />
              <span className="conn-model__id">{m.id}</span>
              <span className="conn-model__uses">{(m.usecases || []).map(u => <span key={u} className="dk-badge">{label(u)}</span>)}</span>
            </label>
          </li>
        ))}
      </ul>
      <div className="conn-actions">
        <Button size="sm" variant="primary" busy={busy} disabled={ticked.length === 0} onClick={add} data-testid="connection-new-add">{t('card.addSelected')}</Button>
        <Button size="sm" variant="ghost" onClick={notNow} data-testid="connection-new-skip">{t('card.notNow')}</Button>
      </div>
    </div>
  )
}

function Card({ connection, onEdit, onChanged, onViewInstalled, replace, addToast }) {
  const { t } = useTranslation('connections')
  const when = useWhen()
  const [refreshing, setRefreshing] = useState(false)
  const status = statusOf(connection)
  const attention = needsAttention(connection)
  const dot = status === 'ok' ? ' dk-dot--ok' : attention ? ' dk-dot--error' : ''
  const statusText = status === 'ok'
    ? t('card.status.ok', { when: when(connection.last_refresh) || t('card.justNow') })
    : t(`card.status.${status}`)

  const refresh = async () => {
    setRefreshing(true)
    try {
      replace(await connectionsApi.refresh(connection.id))
      onChanged?.()
    } catch (err) {
      addToast?.(t('card.refreshFailed', { name: connection.name, message: err.message }), 'error')
    } finally {
      setRefreshing(false)
    }
  }

  const count = connection.entries?.length || 0
  const gone = connection.unavailable?.length || 0
  return (
    <article className="conn-card" data-testid="connection-card" data-status={status} aria-label={connection.name}>
      <header className="conn-card__head">
        <div className="conn-card__title">
          <h2 className="conn-card__name">{connection.name}</h2>
          <span className="dk-badge">{t(`kinds.${connection.kind}`, { defaultValue: connection.kind })}</span>
          <span className="conn-card__host" title={hostOf(connection.url)}>{hostOf(connection.url)}</span>
        </div>
        <div className="conn-card__actions">
          <Button size="sm" icon="refresh" busy={refreshing} onClick={refresh} data-testid="connection-refresh">{t('card.refresh')}</Button>
          <Button size="sm" icon="edit" onClick={() => onEdit(connection)} data-testid="connection-edit">{t('card.edit')}</Button>
        </div>
      </header>
      <p className="conn-card__status" data-testid="connection-status">
        <span className={`dk-dot${dot}`} aria-hidden="true" />
        <span>{statusText}</span>
      </p>

      {attention && (
        <Callout
          tone="error"
          role="alert"
          title={t(status === 'auth_failed' ? 'card.authTitle' : 'card.unreachableTitle')}
          testId="connection-error"
          actions={(
            <Button size="sm" variant="primary" icon={status === 'auth_failed' ? 'key' : 'edit'} onClick={() => onEdit(connection)} data-testid="connection-fix">
              {t(status === 'auth_failed' ? 'card.updateKey' : 'card.editConnection')}
            </Button>
          )}
        >
          {connection.last_error || t(status === 'auth_failed' ? 'card.authText' : 'card.unreachableText')}
        </Callout>
      )}

      {connection.new?.length > 0 && (
        <NewModels key={dismissKey(connection)} connection={connection} addToast={addToast} onAdded={() => { onChanged?.(); }} />
      )}

      <footer className="conn-card__foot">
        <span>
          {t('card.models', { count })}
          {gone > 0 && <span className="conn-card__gone"> {'·'} {t('card.gone', { count: gone })}</span>}
        </span>
        <Button size="sm" variant="ghost" iconEnd="arrow-right" onClick={() => onViewInstalled(connection.id)} data-testid="connection-view">{t('card.view')}</Button>
      </footer>
    </article>
  )
}

// The Connections tab of the Models page: one card for each connection.
export default function ConnectionsView({ state, addToast, onConnect, onChanged, onViewInstalled }) {
  const { t } = useTranslation('connections')
  const { connections, loading, error, refetch, replace } = state
  const [editing, setEditing] = useState(null)

  if (loading && connections.length === 0) return <SkeletonRows count={2} />
  if (error && connections.length === 0) {
    return <LoadError title={t('load.error')} error={{ message: error }} onRetry={refetch} />
  }
  if (connections.length === 0) {
    return (
      <EmptyState
        title={t('empty.title')}
        body={t('empty.body')}
        actions={<Button variant="primary" icon="plug" onClick={onConnect} data-testid="connections-empty-connect">{t('sheet.open')}</Button>}
      />
    )
  }

  return (
    <div className="conn-list" data-testid="connections-list">
      {connections.map(c => (
        <Card key={c.id} connection={c} onEdit={setEditing} onChanged={() => { refetch(); onChanged?.() }} onViewInstalled={onViewInstalled} replace={replace} addToast={addToast} />
      ))}
      {editing && (
        <EditConnectionSheet
          connection={editing}
          onClose={() => setEditing(null)}
          onSaved={(updated, { keyChanged }) => {
            replace(updated)
            setEditing(null)
            addToast?.(t(keyChanged ? 'edit.savedKey' : 'edit.saved', { name: updated.name }), 'success')
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
