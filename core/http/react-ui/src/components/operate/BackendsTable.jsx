/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { cssVars } from '../../utils/modelLedger'
import { versionLabel } from '../../utils/backendRows'
import Icon from '../Icon'

function SortHead({ col, label, sort, order, onSort, className }) {
  const active = sort === col
  return (
    <th scope="col" className={className} aria-sort={active ? (order === 'asc' ? 'ascending' : 'descending') : undefined}>
      <button type="button" className="dk-table-sort" onClick={() => onSort(col)}>
        {label}
        <Icon name={active && order === 'desc' ? 'arrow-down' : 'arrow-up'} />
      </button>
    </th>
  )
}

// The state of a backend as words, a mark and, while an install runs, a bar.
// Colour never says it alone: every state has a word.
export function StateCell({ state }) {
  const { t } = useTranslation('operate')
  switch (state.kind) {
    case 'installing':
      return (
        <span className="bk-state bk-state--busy" role="status">
          <span
            className="dk-progress bk-state__bar"
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={state.progress}
            aria-label={t('backends.state.progress')}
            style={cssVars({ '--dk-value': `${state.progress}%` })}
            data-indeterminate={state.progress > 0 ? undefined : ''}
          >
            <span className="dk-progress-bar" />
          </span>
          <span className="dk-mono bk-state__pct">{state.progress > 0 ? `${state.progress}%` : t('backends.state.starting')}</span>
        </span>
      )
    case 'queued':
      return <span className="bk-state bk-state--busy"><Icon name="clock" /> {t('backends.state.queued')}</span>
    case 'removing':
      return <span className="bk-state bk-state--busy"><Icon name="trash" /> {t('backends.state.removing')}</span>
    case 'failed':
      return <span className="bk-state bk-state--error" title={state.error}><Icon name="alert-circle" /> {t('backends.state.failed')}</span>
    case 'update':
      return (
        <span className="bk-state bk-state--warn">
          <Icon name="arrow-up" /> {state.to ? t('backends.state.updateTo', { version: state.to }) : t('backends.state.update')}
        </span>
      )
    case 'current':
      return <span className="bk-state bk-state--ok"><Icon name="check-circle" /> {t('backends.state.current')}</span>
    default:
      return <span className="bk-state bk-state--muted">{t('backends.state.absent')}</span>
  }
}

// One table for both lists. A row is a name, a version, a state and the one
// thing to do about it; the rest opens in a detail row under it. The open row
// is the selected backend, so it lives in the URL and survives a reload.
//
//   rows   [{ name, description, version, state, nodes, badge, action, detail }]
export default function BackendsTable({
  rows, expanded, onToggle, showNodes, sort, order, onSort, caption, busy, children,
}) {
  const { t } = useTranslation('operate')
  const baseId = useId()
  const cols = 4 + (showNodes ? 1 : 0)
  return (
    <div className="dk-table-wrap bk-wrap" data-testid="backends-table">
      <table className="dk-table bk-table" aria-busy={busy ? 'true' : undefined}>
        <caption className="dk-sr-only">{caption}</caption>
        <thead>
          <tr>
            {onSort
              ? <SortHead col="name" label={t('backends.columns.backend')} sort={sort} order={order} onSort={onSort} />
              : <th scope="col">{t('backends.columns.backend')}</th>}
            <th scope="col" className="dk-hide-phone">{t('backends.columns.version')}</th>
            {onSort
              ? <SortHead col="status" label={t('backends.columns.state')} sort={sort} order={order} onSort={onSort} />
              : <th scope="col">{t('backends.columns.state')}</th>}
            {showNodes && <th scope="col" className="dk-hide-phone">{t('backends.columns.nodes')}</th>}
            <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('backends.columns.actions')}</span></th>
          </tr>
        </thead>
        <tbody>
          {rows.flatMap((row, index) => {
            const open = expanded === row.name
            const detailId = `${baseId}-d${index}`
            const main = (
              <tr
                key={row.name}
                data-row
                data-clickable
                data-entity={row.name}
                data-testid="backend-row"
                data-selected={open ? 'true' : 'false'}
                data-error={row.state.kind === 'failed' ? '' : undefined}
                onClick={() => onToggle(row.name)}
              >
                <td>
                  <span className="dk-table-name dk-mono">{row.name}</span>
                  <span className="dk-table-sub" title={row.state.kind === 'failed' ? row.state.error : row.description}>
                    {row.state.kind === 'failed' ? row.state.error : (row.description || ' ')}
                  </span>
                </td>
                <td className="dk-hide-phone dk-mono bk-version">{versionLabel(row.version) || '—'}</td>
                <td><StateCell state={row.state} />{row.badge}</td>
                {showNodes && <td className="dk-hide-phone">{row.nodes}</td>}
                <td className="dk-table-actions">
                  {/* A control in the row must not also open the row. */}
                  <span className="bk-acts" onClick={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}>
                    {row.action}
                    <button
                      type="button"
                      className="dk-table-toggle"
                      aria-expanded={open}
                      aria-controls={detailId}
                      aria-label={t(open ? 'backends.hideDetails' : 'backends.showDetails', { name: row.name })}
                      onClick={() => onToggle(row.name)}
                    >
                      <Icon name="chevron-right" />
                    </button>
                  </span>
                </td>
              </tr>
            )
            const detail = (
              <tr key={`${row.name}:detail`} className="dk-table-detail" id={detailId} data-testid={open ? 'backend-detail' : undefined}>
                <td colSpan={cols}>
                  <div className="dk-collapse" data-open={open ? 'true' : 'false'}>
                    <div>
                      <div className="dk-table-detail-body">{open ? row.detail : null}</div>
                    </div>
                  </div>
                </td>
              </tr>
            )
            return [main, detail]
          })}
          {children}
        </tbody>
      </table>
    </div>
  )
}
