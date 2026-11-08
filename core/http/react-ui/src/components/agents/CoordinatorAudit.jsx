/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, EmptyState, LoadError, SkeletonRows, StatusChip } from '../ui'
import { AUDIT_KINDS, AUDIT_OUTCOMES, auditFilterActive, auditParams } from '../../utils/coordinator'

const OUTCOME_TONE = { sent: 'ok', denied: 'warn', info: 'idle' }
const PAGE = 25

// The audit log of one coordinator, or of all of them for an administrator.
//   load(query)      returns { entries, next_before }; query is auditParams(...)
//   coordinators     [{ id, name }] for the coordinator filter and the name column
//   showCoordinator  draw the filter and the column (the administrator's view)
// Newest first, filtered on the server, older rows on demand. A failed read
// says so in place of the table and never looks like an empty log.
export default function CoordinatorAudit({ load, coordinators = [], showCoordinator = false, testId = 'coordinator-audit', labelId = 'coordinator-audit-h' }) {
  const { t } = useTranslation('agents')
  const [filters, setFilters] = useState({ kind: '', outcome: '', since: '', until: '', coordinator: '' })
  const [rows, setRows] = useState([])
  const [next, setNext] = useState(0)
  const [status, setStatus] = useState('loading')
  const [error, setError] = useState(null)
  const [more, setMore] = useState(false)
  // A slow answer to an old filter must not overwrite a newer one.
  const seq = useRef(0)
  const loadRef = useRef(load)
  loadRef.current = load

  const read = useCallback(async (f, before) => {
    const mine = ++seq.current
    try {
      const res = await loadRef.current(auditParams({ ...f, limit: PAGE, before }))
      if (mine !== seq.current) return
      setRows(prev => (before ? [...prev, ...(res.entries || [])] : (res.entries || [])))
      setNext(res.next_before || 0)
      setStatus('ready')
      setError(null)
    } catch (err) {
      if (mine !== seq.current) return
      setError(err)
      if (!before) setStatus('error')
    } finally {
      if (mine === seq.current) setMore(false)
    }
  }, [])

  useEffect(() => { setStatus('loading'); read(filters, 0) }, [filters, read])

  const set = (patch) => setFilters(f => ({ ...f, ...patch }))
  const names = Object.fromEntries(coordinators.map(c => [c.id, c.name]))
  const reason = (r) => t(`coordinator.audit.reason.${r}`, { defaultValue: r })
  const active = auditFilterActive(filters)

  const select = (id, label, value, options, labelOf) => (
    <div className="crd-filter">
      <label className="dk-label" htmlFor={id}>{label}</label>
      <span className="dk-select-wrap">
        <select id={id} className="dk-select" value={value} onChange={(e) => set({ [id.replace('crd-f-', '')]: e.target.value })} data-testid={id}>
          <option value="">{t('coordinator.audit.any')}</option>
          {options.map(o => <option key={o.id ?? o} value={o.id ?? o}>{labelOf(o)}</option>)}
        </select>
      </span>
    </div>
  )

  return (
    <div className="crd-audit" data-testid={testId}>
      <form className="crd-filters" onSubmit={(e) => e.preventDefault()} aria-label={t('coordinator.audit.filters')}>
        {showCoordinator && select('crd-f-coordinator', t('coordinator.audit.coordinator'), filters.coordinator, coordinators, c => c.name)}
        {select('crd-f-kind', t('coordinator.audit.kind'), filters.kind, AUDIT_KINDS, k => t(`coordinator.audit.kinds.${k}`))}
        {select('crd-f-outcome', t('coordinator.audit.outcome'), filters.outcome, AUDIT_OUTCOMES, o => t(`coordinator.audit.outcomes.${o}`))}
        <div className="crd-filter">
          <label className="dk-label" htmlFor="crd-f-since">{t('coordinator.audit.since')}</label>
          <input id="crd-f-since" type="date" className="dk-input" value={filters.since} max={filters.until || undefined} onChange={(e) => set({ since: e.target.value })} data-testid="crd-f-since" />
        </div>
        <div className="crd-filter">
          <label className="dk-label" htmlFor="crd-f-until">{t('coordinator.audit.until')}</label>
          <input id="crd-f-until" type="date" className="dk-input" value={filters.until} min={filters.since || undefined} onChange={(e) => set({ until: e.target.value })} data-testid="crd-f-until" />
        </div>
        {active && <Button variant="ghost" size="sm" onClick={() => setFilters({ kind: '', outcome: '', since: '', until: '', coordinator: '' })} data-testid="crd-f-clear">{t('coordinator.audit.clear')}</Button>}
      </form>

      {status === 'loading' ? <SkeletonRows count={3} />
        : status === 'error' ? <LoadError title={t('coordinator.audit.loadFailed')} error={error} onRetry={() => { setStatus('loading'); return read(filters, 0) }} />
          : rows.length === 0 ? (
            <EmptyState
              variant={active ? 'no-results' : 'first-run'}
              title={active ? t('coordinator.audit.noMatch') : t('coordinator.audit.none')}
              body={active ? undefined : t('coordinator.audit.noneBody')}
              level={3}
              testId="coordinator-audit-empty"
            />
          ) : (
            <>
              <div className="dk-table-wrap" role="region" aria-labelledby={labelId} tabIndex={0}>
                <table className="dk-table crd-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('coordinator.audit.when')}</th>
                      {showCoordinator && <th scope="col">{t('coordinator.audit.coordinator')}</th>}
                      <th scope="col">{t('coordinator.audit.kind')}</th>
                      <th scope="col">{t('coordinator.audit.outcome')}</th>
                      <th scope="col">{t('coordinator.audit.reasonCol')}</th>
                      <th scope="col">{t('coordinator.audit.who')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map(e => (
                      <tr key={e.id} data-testid="coordinator-audit-row" data-outcome={e.outcome}>
                        <td className="crd-when"><time dateTime={e.at}>{new Date(e.at).toLocaleString()}</time></td>
                        {showCoordinator && <td>{names[e.coordinator_id] || e.coordinator_id}<span className="crd-sub">{e.owner}</span></td>}
                        <td>{t(`coordinator.audit.kinds.${e.kind}`, { defaultValue: e.kind })}</td>
                        <td><StatusChip tone={OUTCOME_TONE[e.outcome] || 'idle'} dot>{t(`coordinator.audit.outcomes.${e.outcome}`, { defaultValue: e.outcome })}</StatusChip></td>
                        <td>{reason(e.reason)}</td>
                        <td>
                          {[e.actor, e.target].filter(Boolean).join(' → ') || '—'}
                          {(e.via || e.length > 0) && (
                            <span className="crd-sub">
                              {[e.via && t(`coordinator.audit.via.${e.via}`, { defaultValue: e.via }), e.length > 0 && t('coordinator.audit.chars', { count: e.length })].filter(Boolean).join(' · ')}
                            </span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {error && <LoadError title={t('coordinator.audit.moreFailed')} error={error} onRetry={() => { setMore(true); return read(filters, next) }} />}
              {next > 0 && !error && (
                <div className="crd-more">
                  <Button size="sm" busy={more} onClick={() => { setMore(true); read(filters, next) }} data-testid="coordinator-audit-more">{t('coordinator.audit.older')}</Button>
                </div>
              )}
            </>
          )}
    </div>
  )
}
