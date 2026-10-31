/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout, LoadError, SkeletonRows, Switch } from '../ui'
import { chatEgressApi } from '../../utils/api'
import { FIELD_BY_KEY, getValue, setValue } from '../../utils/settingsSchema'
import { EGRESS_CONFIRM_KEY, EGRESS_PRIVATE_PHRASE } from '../../utils/settingsPending'

// Settings > Tools: the network of the chats' Sandbox commands, from GET
// /api/chats/admin/egress. The limit, the default, the ports, the extra
// registries and the retention are rows of the page; this section shows who
// serves commands, the registries list, the private-network switch with its
// typed phrase, and the audit. The switch is a draft of the page, applied with
// the other settings; the phrase travels with it and is not stored.

const PRIVATE = FIELD_BY_KEY.chats_egress_allow_private
const VERDICTS = ['', 'allow', 'block', 'done']

function PrivateSwitch({ settings, initialOn, onChange }) {
  const { t } = useTranslation('admin')
  const labelId = useId()
  const phraseId = useId()
  const on = !!getValue(PRIVATE, settings)
  const needsPhrase = on && !initialOn
  return (
    <div className="st-tools__controls" data-testid="settings-egress-private">
      <div className="st-tools__switch">
        <Switch checked={on} labelledBy={labelId} onChange={(v) => onChange(setValue(PRIVATE, settings, v))} testId="settings-egress-private-switch" />
        <span id={labelId}>{t('settings.egress.private')}</span>
      </div>
      <p className="st-row__desc" data-tone="warn">{t('settings.egress.privateWarning')}</p>
      {needsPhrase && (
        <label className="st-tools__controls" htmlFor={phraseId}>
          <span className="st-row__desc">{t('settings.egress.typePhrase', { phrase: EGRESS_PRIVATE_PHRASE })}</span>
          <input
            id={phraseId} className="dk-input" type="text" autoComplete="off" spellCheck={false}
            value={settings?.[EGRESS_CONFIRM_KEY] || ''}
            onChange={(e) => onChange({ ...settings, [EGRESS_CONFIRM_KEY]: e.target.value })}
            data-testid="settings-egress-private-phrase"
          />
        </label>
      )}
    </div>
  )
}

function AuditTable() {
  const { t } = useTranslation('admin')
  const [filter, setFilter] = useState({ verdict: '', host: '', user: '' })
  const [state, setState] = useState({ loading: true, events: [], error: '' })
  const [reload, setReload] = useState(0)
  useEffect(() => {
    let live = true
    setState(s => ({ ...s, loading: true }))
    const params = { limit: 200 }
    for (const [k, v] of Object.entries(filter)) if (v.trim()) params[k] = v.trim()
    chatEgressApi.events(params)
      .then(res => { if (live) setState({ loading: false, events: res?.events || [], error: '' }) })
      .catch(e => { if (live) setState({ loading: false, events: [], error: e.message || 'error' }) })
    return () => { live = false }
  }, [filter, reload])
  return (
    <div className="st-tools__controls" data-testid="settings-egress-audit">
      <div className="st-row__meta">
        <label>
          <span className="dk-sr-only">{t('settings.egress.audit.verdict')}</span>
          <select className="dk-select" value={filter.verdict} onChange={e => setFilter(f => ({ ...f, verdict: e.target.value }))} data-testid="settings-egress-audit-verdict">
            {VERDICTS.map(v => <option key={v} value={v}>{t(`settings.egress.audit.verdicts.${v || 'any'}`)}</option>)}
          </select>
        </label>
        <input className="dk-input" type="search" placeholder={t('settings.egress.audit.host')} aria-label={t('settings.egress.audit.host')}
          value={filter.host} onChange={e => setFilter(f => ({ ...f, host: e.target.value }))} />
        <input className="dk-input" type="search" placeholder={t('settings.egress.audit.user')} aria-label={t('settings.egress.audit.user')}
          value={filter.user} onChange={e => setFilter(f => ({ ...f, user: e.target.value }))} />
        <Button size="sm" variant="ghost" onClick={() => setReload(n => n + 1)}>{t('settings.egress.audit.refresh')}</Button>
      </div>
      {state.loading && <SkeletonRows rows={2} />}
      {!state.loading && state.error && <LoadError title={t('settings.egress.audit.loadFailed')} error={state.error} onRetry={() => setReload(n => n + 1)} />}
      {!state.loading && !state.error && state.events.length === 0 && <p className="st-note">{t('settings.egress.audit.empty')}</p>}
      {!state.loading && !state.error && state.events.length > 0 && (
        <div className="dk-table-wrap">
          <table className="dk-table dk-table--compact">
            <thead>
              <tr>
                <th scope="col">{t('settings.egress.audit.at')}</th>
                <th scope="col">{t('settings.egress.audit.user')}</th>
                <th scope="col">{t('settings.egress.audit.host')}</th>
                <th scope="col">{t('settings.egress.audit.verdict')}</th>
                <th scope="col">{t('settings.egress.audit.reason')}</th>
                <th scope="col">{t('settings.egress.audit.bytes')}</th>
              </tr>
            </thead>
            <tbody>
              {state.events.map((e, i) => (
                <tr key={`${e.at}-${i}`} data-verdict={e.verdict}>
                  <td>{new Date(e.at).toLocaleString()}</td>
                  <td>{e.user_name || e.user}</td>
                  <td><code className="st-key">{e.host}:{e.port}</code></td>
                  <td>{t(`settings.egress.audit.verdicts.${e.verdict}`, { defaultValue: e.verdict })}</td>
                  <td><code className="st-key">{e.reason}</code></td>
                  <td>{e.bytes_up || e.bytes_down ? `${e.bytes_up || 0} / ${e.bytes_down || 0}` : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export default function EgressSection({ settings, initial, onChange }) {
  const { t } = useTranslation('admin')
  const [state, setState] = useState({ loading: true, info: null, error: '' })
  const [reload, setReload] = useState(0)
  const [audit, setAudit] = useState(false)

  useEffect(() => {
    let live = true
    setState(s => ({ ...s, loading: true, error: '' }))
    chatEgressApi.admin()
      .then(res => { if (live) setState({ loading: false, info: res, error: '' }) })
      .catch(e => { if (live) setState({ loading: false, info: null, error: e.message || 'error' }) })
    return () => { live = false }
  }, [reload])

  if (state.loading) return <SkeletonRows rows={2} />
  if (state.error) return <LoadError title={t('settings.egress.loadFailed')} error={state.error} onRetry={() => setReload(n => n + 1)} />
  const info = state.info || {}
  const serves = info.sandbox
    ? t(info.isolation === 'host' ? 'settings.egress.servesHost' : 'settings.egress.serves', { name: info.sandbox })
    : t('settings.egress.servesNone')
  return (
    <div className="st-list st-tools" id="settings-egress" data-testid="settings-egress">
      <div className="st-row st-tools__row" data-wide data-testid="settings-egress-row">
        <div className="st-row__main">
          <div className="st-row__title">
            <span className="st-row__label">{t('settings.egress.title')}</span>
            <span className="st-tag" data-testid="settings-egress-serves">{serves}</span>
          </div>
          <p className="st-row__desc">{t('settings.egress.intro')}</p>
          {info.isolation === 'host' && <Callout tone="warn">{t('settings.egress.hostNote')}</Callout>}
          <p className="st-row__desc" data-testid="settings-egress-counts">{t('settings.egress.counts', { allowed: info.allowed_24h || 0, blocked: info.blocked_24h || 0 })}</p>
          <div className="st-row__meta"><code className="st-key">chats_egress_allow_private</code></div>
          <PrivateSwitch settings={settings} initialOn={!!getValue(PRIVATE, initial)} onChange={onChange} />
          <details className="st-tools__controls" data-testid="settings-egress-registries">
            <summary>{t('settings.egress.registries', { count: (info.registries || []).length })}</summary>
            <ul className="st-tools__choice">
              {(info.registries || []).map(r => (
                <li key={r.id} className="st-row__desc"><b>{r.title}</b>: <code className="st-key">{r.hosts.join(', ')}</code></li>
              ))}
            </ul>
          </details>
          <div>
            <Button size="sm" variant="ghost" aria-expanded={audit} onClick={() => setAudit(v => !v)} data-testid="settings-egress-audit-toggle">
              {t(audit ? 'settings.egress.hideAudit' : 'settings.egress.showAudit')}
            </Button>
          </div>
          {audit && <AuditTable />}
        </div>
      </div>
    </div>
  )
}
