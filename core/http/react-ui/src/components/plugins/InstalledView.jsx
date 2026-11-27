/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ActionMenu from '../ActionMenu'
import ConfirmDialog from '../ConfirmDialog'
import { AppIcon } from '../addons/AddonBits'
import { Button, Callout, EmptyState, LoadError, PaneLayout, SkeletonRows, StatusChip, Switch, Tag } from '../ui'
import { Sha } from './PluginBits'
import PluginDetails from './PluginDetails'
import PluginSettings from './PluginSettings'
import { errorKey, groupInstalls, installChip, installRights } from './pluginState'

// The installs the person can see, in two groups: for everyone and only for
// them. Each row has the person's own switch, Update when a newer version
// waits, and a menu with Details, Settings, Roll back and Remove.
//
// An install for everyone starts off for each person. One whose update added
// code is off until the person turns it on again, which accepts the code
// (the server answers 409 needs_reenable to a plain turn on).
//
//   list        { status, data, error, retry } of the installs
//   ops         plugin operations by plugin ID
//   onChanged() the list is read again
//   onUpdate(install), onBrowse()
//   openId      an install whose details open first (a link from a connector)
export default function InstalledView({ list, api, isAdmin, userId, ops, onChanged, onUpdate, onBrowse, addToast, openId = '' }) {
  const { t } = useTranslation('addons')
  const [busy, setBusy] = useState({})
  const [optimistic, setOptimistic] = useState({})
  const [error, setError] = useState('')
  const [reenable, setReenable] = useState(null)
  const [toRemove, setToRemove] = useState(null)
  const [toRollback, setToRollback] = useState(null)
  const [pending, setPending] = useState(false)
  const [detail, setDetail] = useState(null)
  const [settings, setSettings] = useState(null)

  const opened = useRef('')
  useEffect(() => {
    if (!openId || opened.current === openId || list.status !== 'ready') return
    opened.current = openId
    const hit = (list.data || []).find((i) => i.id === openId)
    if (hit) setDetail(hit)
  }, [openId, list.status, list.data])

  const say = (e) => (errorKey(e) ? t(errorKey(e), { defaultValue: e.message }) : e?.message || t('plugins.errors.generic'))

  const setEnabled = async (inst, on, { again = false } = {}) => {
    setError('')
    setBusy((b) => ({ ...b, [inst.id]: true }))
    setOptimistic((o) => ({ ...o, [inst.id]: on }))
    try {
      await api.setMe(inst.id, { enabled: on, ...(again ? { reenable: true } : {}) })
      onChanged()
    } catch (e) {
      if (e?.type === 'needs_reenable' && on && !again) setReenable(inst)
      else setError(say(e))
    } finally {
      setBusy((b) => ({ ...b, [inst.id]: false }))
      setOptimistic((o) => { const n = { ...o }; delete n[inst.id]; return n })
    }
  }

  const act = async (fn, okText) => {
    setPending(true)
    setError('')
    try {
      await fn()
      if (okText) addToast?.(okText, 'success')
      onChanged()
    } catch (e) {
      setError(say(e))
    } finally {
      setPending(false)
      setToRemove(null)
      setToRollback(null)
    }
  }

  if (list.status === 'loading') return <SkeletonRows count={3} label={t('plugins.installed.loading')} />
  if (list.status === 'error') return <LoadError title={t('plugins.installed.loadFailed')} error={list.error} onRetry={list.retry} testId="plugins-installed-error" />
  const installs = list.data || []
  if (installs.length === 0) {
    return (
      <EmptyState icon="puzzle" title={t('plugins.installed.emptyTitle')} body={t('plugins.installed.emptyBody')} testId="plugins-installed-empty"
        actions={<Button variant="primary" icon="store" onClick={onBrowse}>{t('plugins.installed.browse')}</Button>} />
    )
  }
  const groups = groupInstalls(installs)
  const row = (inst) => {
    const rights = installRights(inst, { isAdmin, userId })
    const chip = installChip(inst)
    const enabled = inst.id in optimistic ? optimistic[inst.id] : inst.enabled
    const op = ops.get(inst.plugin)
    const items = [
      { key: 'details', icon: 'info', label: t('plugins.installed.details'), onClick: () => setDetail(inst) },
      { key: 'settings', icon: 'sliders', label: t('plugins.installed.settings'), onClick: () => setSettings(inst) },
      { key: 'rollback', icon: 'undo', label: t('plugins.installed.rollback'), hidden: !rights.rollback, onClick: () => setToRollback(inst) },
      { divider: true, key: 'divider', hidden: !rights.remove },
      { key: 'remove', icon: 'trash', label: t('plugins.installed.remove'), danger: true, hidden: !rights.remove, onClick: () => setToRemove(inst) },
    ]
    return (
      <li key={inst.id} className="ad-tile pl-tile" data-testid="plugin-row" data-id={inst.id} data-plugin={inst.plugin} data-on={enabled ? 'true' : 'false'} data-selected={detail?.id === inst.id ? 'true' : undefined}>
        <button type="button" className="ad-tile__open" aria-pressed={detail?.id === inst.id} aria-label={t('plugins.installed.open', { name: inst.name || inst.plugin })} onClick={() => setDetail(inst)}>
          <AppIcon id={inst.plugin} name={inst.name} size={36} />
          <span className="ad-tile__text">
            <span className="ad-tile__head">
              <span className="ad-tile__title">{inst.name || inst.plugin}</span>
              {chip && <StatusChip status={chip.status} tone={chip.tone} label={t(`plugins.installed.state.${chip.key}`)} testId="plugin-row-state" />}
              {op && <StatusChip status="installing" label={t('plugins.installed.working')} />}
            </span>
            {inst.description && <span className="ad-tile__desc">{inst.description}</span>}
            <span className="ad-meta">
              {inst.version && <span className="ad-meta__item ad-version">{inst.version}</span>}
              <span className="ad-meta__item"><Sha sha={inst.sha} /></span>
              <span className="ad-meta__item pl-mono">{inst.plugin}</span>
              {inst.runsCode && <span className="ad-meta__item"><Tag tone="warn" icon="code">{t('plugins.adds.runsCode')}</Tag></span>}
            </span>
          </span>
        </button>
        <div className="ad-tile__side">
          {rights.update && (
            <span className="ad-tile__act">
              <Button size="sm" icon="arrow-up" onClick={() => onUpdate(inst)} disabled={Boolean(op)} data-testid="plugin-row-update">{t('plugins.installed.update')}</Button>
            </span>
          )}
          <Switch checked={Boolean(enabled) && !inst.needsReenable} disabled={Boolean(busy[inst.id]) || inst.state === 'blocked' && !enabled}
            onChange={(on) => setEnabled(inst, on)} label={t('plugins.installed.toggle', { name: inst.name || inst.plugin })} testId="plugin-row-switch" />
          <ActionMenu items={items} ariaLabel={t('plugins.installed.menu', { name: inst.name || inst.plugin })} compact />
        </div>
        {inst.needsReenable && (
          <div className="ad-tile__upgrade">
            <Callout tone="warn" icon="code" title={t('plugins.installed.reenableTitle')} testId="plugin-row-reenable"
              actions={<Button size="sm" onClick={() => setReenable(inst)} data-testid="plugin-row-reenable-button">{t('plugins.installed.reenable')}</Button>}>
              {t('plugins.installed.reenableBody')}
            </Callout>
          </div>
        )}
      </li>
    )
  }

  return (
    <PaneLayout className="pl-layout">
      <div className="ad-groups pl-installed" data-testid="plugins-installed">
        {error && (
          <Callout tone="error" role="alert" testId="plugins-installed-action-error"
            actions={<Button variant="ghost" size="sm" onClick={() => setError('')}>{t('plugins.dismiss')}</Button>}>{error}</Callout>
        )}
        {[['instance', groups.instance], ['user', groups.user]].filter(([, g]) => g.length > 0).map(([key, g]) => (
          <section key={key} className="ad-group" aria-labelledby={`pl-group-${key}`} data-testid={`plugins-group-${key}`}>
            <h3 className="ad-group__title" id={`pl-group-${key}`}>{t(`plugins.installed.group.${key}`)}</h3>
            {key === 'instance' && <p className="pl-quiet">{t('plugins.installed.instanceNote')}</p>}
            <ul className="ad-tiles">{g.map(row)}</ul>
          </section>
        ))}
      </div>
      {detail && <PluginDetails key={detail.id} install={detail} api={api} onClose={() => setDetail(null)} onSettings={setSettings} />}
      {settings && <PluginSettings install={settings} api={api} onClose={() => setSettings(null)} />}
      <ConfirmDialog
        open={Boolean(reenable)}
        title={reenable ? t('plugins.installed.reenableConfirmTitle', { name: reenable.name || reenable.plugin }) : ''}
        message={t('plugins.installed.reenableConfirm')}
        confirmLabel={t('plugins.installed.reenable')}
        onConfirm={() => { const inst = reenable; setReenable(null); setEnabled(inst, true, { again: true }) }}
        onCancel={() => setReenable(null)}
      />
      <ConfirmDialog
        open={Boolean(toRemove)}
        danger
        pending={pending}
        title={toRemove ? t('plugins.installed.removeTitle', { name: toRemove.name || toRemove.plugin }) : ''}
        message={toRemove?.scope === 'instance' ? t('plugins.installed.removeEveryone') : t('plugins.installed.removeMine')}
        confirmLabel={t('plugins.installed.remove')}
        onConfirm={() => act(() => api.remove(toRemove.id), t('plugins.installed.removed', { name: toRemove.name || toRemove.plugin }))}
        onCancel={() => setToRemove(null)}
      />
      <ConfirmDialog
        open={Boolean(toRollback)}
        pending={pending}
        title={toRollback ? t('plugins.installed.rollbackTitle', { name: toRollback.name || toRollback.plugin }) : ''}
        message={t('plugins.installed.rollbackBody')}
        confirmLabel={t('plugins.installed.rollback')}
        onConfirm={() => act(() => api.rollback(toRollback.id), t('plugins.installed.rolledBack', { name: toRollback.name || toRollback.plugin }))}
        onCancel={() => setToRollback(null)}
      />
    </PaneLayout>
  )
}
