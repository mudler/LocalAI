/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'

const MARK = {
  ok: 'check-circle',
  warn: 'warning',
  error: 'alert-circle',
  idle: 'circle',
  active: 'circle-dot',
}

// One row of the Status ledger: a mark, a name, one line, and a disclosure.
// A row with a problem arrives open; every other row is one line. The state
// is said in words as well as in the mark, so colour never carries it alone.
export default function LedgerRow({ id, level, open, onToggle, title, summary, children, loading = false }) {
  const { t } = useTranslation('operate')
  const bodyId = useId()
  return (
    <li className="op-row" data-level={level} data-open={open ? 'true' : 'false'} data-loading={loading ? 'true' : undefined} data-testid={`operate-row-${id}`}>
      <button
        type="button"
        className="op-row__head"
        aria-expanded={open}
        aria-controls={bodyId}
        disabled={loading}
        onClick={onToggle}
      >
        <span className="op-row__mark" aria-hidden="true"><Icon name={MARK[level] || MARK.idle} /></span>
        <span className="op-row__name">{title}</span>
        <span className="op-row__summary">{summary}</span>
        <span className="op-row__toggle">
          {open ? t('status.close') : t('status.open')}
          <Icon name="chevron-down" />
        </span>
      </button>
      <div className="dk-collapse" data-open={open ? 'true' : 'false'} id={bodyId}>
        <div className="op-row__clip">
          <div className="op-row__body">{children}</div>
        </div>
      </div>
    </li>
  )
}
