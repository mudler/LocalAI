import { useTranslation } from 'react-i18next'
import { noFilters } from '../../utils/chatSearch'

const ORIGINS = ['person', 'automated', 'trigger', 'connector', 'job', 'page']
const RANGES = ['day', 'week', 'month', 'year', 'custom']

// The filters of the chat search: agent, where the chat started and when. A
// change calls onChange with the new filters; the list searches again.
export default function SearchFilters({ filters, onChange, agents, idPrefix, active }) {
  const { t } = useTranslation('chat')
  const set = (patch) => onChange({ ...filters, ...patch })
  const id = (name) => `${idPrefix}-find-${name}`
  return (
    <div className="cx-find-filters" role="group" aria-label={t('menu.find.filters')} data-testid="chats-filters">
      {agents.length > 0 && (
        <label className="cx-find-filter" htmlFor={id('agent')}>
          <span className="cx-find-filter__label">{t('menu.find.agent')}</span>
          <select id={id('agent')} className="cx-find-select" value={filters.agent} onChange={(e) => set({ agent: e.target.value })} data-testid="chats-filter-agent">
            <option value="">{t('menu.find.anyAgent')}</option>
            {agents.map((a) => <option key={a.id} value={a.id}>{a.name || a.id}</option>)}
          </select>
        </label>
      )}
      <label className="cx-find-filter" htmlFor={id('origin')}>
        <span className="cx-find-filter__label">{t('menu.find.origin')}</span>
        <select id={id('origin')} className="cx-find-select" value={filters.origin} onChange={(e) => set({ origin: e.target.value })} data-testid="chats-filter-origin">
          <option value="">{t('menu.find.anyOrigin')}</option>
          {ORIGINS.map((o) => <option key={o} value={o}>{t(`menu.find.originName.${o}`)}</option>)}
        </select>
      </label>
      <label className="cx-find-filter" htmlFor={id('range')}>
        <span className="cx-find-filter__label">{t('menu.find.when')}</span>
        <select id={id('range')} className="cx-find-select" value={filters.range} onChange={(e) => set({ range: e.target.value })} data-testid="chats-filter-range">
          <option value="">{t('menu.find.anyTime')}</option>
          {RANGES.map((r) => <option key={r} value={r}>{t(`menu.find.range.${r}`)}</option>)}
        </select>
      </label>
      {filters.range === 'custom' && (
        <>
          <label className="cx-find-filter" htmlFor={id('from')}>
            <span className="cx-find-filter__label">{t('menu.find.from')}</span>
            <input id={id('from')} type="date" className="cx-find-select" value={filters.from} max={filters.to || undefined} onChange={(e) => set({ from: e.target.value })} data-testid="chats-filter-from" />
          </label>
          <label className="cx-find-filter" htmlFor={id('to')}>
            <span className="cx-find-filter__label">{t('menu.find.to')}</span>
            <input id={id('to')} type="date" className="cx-find-select" value={filters.to} min={filters.from || undefined} onChange={(e) => set({ to: e.target.value })} data-testid="chats-filter-to" />
          </label>
        </>
      )}
      {active && (
        <button type="button" className="cx-btn cx-btn--ghost cx-find-reset" onClick={() => onChange(noFilters)} data-testid="chats-filter-reset">
          {t('menu.find.reset')}
        </button>
      )}
    </div>
  )
}
