/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button } from '../ui'
import { SwitchRow } from '../chat/SwitchList'
import { SEARCH_MIN } from '../../utils/chatSkills'
import { filterMcp } from '../../utils/agentUses'
import './agentuses.css'

// The MCP servers the agent may use: every connector the user has defined and
// every server of the fixed model, one row each with a switch, its status and a
// search field from nine rows on. There is no free text: a name the agent holds
// that nothing defined matches is an amber "not defined" row with a remove
// button. rows come from mcpRows; onSet(row, on) changes the stored list.
export default function AgentMcpPicker({ rows, onSet }) {
  const { t } = useTranslation('agents')
  const [query, setQuery] = useState('')
  const searchable = rows.length >= SEARCH_MIN
  const shown = filterMcp(rows, searchable ? query : '')
  return (
    <div className="au-mcp" data-testid="agent-mcp-picker">
      {searchable && (
        <div className="swl-search">
          <Icon name="search" className="swl-search__icon" />
          <input type="search" className="dk-input swl-search__input" value={query} onChange={(e) => setQuery(e.target.value)}
            placeholder={t('create.uses.mcp.search')} aria-label={t('create.uses.mcp.search')} data-testid="agent-mcp-search" />
        </div>
      )}
      {rows.length === 0 && <p className="ag-note" data-testid="agent-mcp-empty">{t('create.uses.mcp.none')}</p>}
      <div className="au-list">
        {shown.map((r) => (
          <SwitchRow
            key={r.key}
            name={r.name}
            blurb={r.kind === 'unknown' ? undefined : t(r.kind === 'connector' ? 'create.uses.mcp.connector' : 'create.uses.mcp.server')}
            desc={r.kind === 'unknown' ? t('create.uses.mcp.unknown') : r.hint || (r.unverified ? t('create.uses.mcp.unverified') : '')}
            tone={r.kind === 'unknown' ? 'warn' : undefined}
            stacked={r.kind === 'unknown' || Boolean(r.hint)}
            checked={r.on}
            onChange={(on) => onSet(r, on)}
            testId="agent-mcp-row"
            data={{ kind: r.kind, ref: r.ref }}
            extra={r.kind === 'unknown' ? (
              <div className="au-body">
                <Button size="sm" icon="close" onClick={() => onSet(r, false)} aria-label={t('create.uses.mcp.remove', { name: r.name })} data-testid="agent-mcp-remove">{t('create.uses.mcp.removeShort')}</Button>
              </div>
            ) : undefined}
          />
        ))}
        {rows.length > 0 && shown.length === 0 && <p className="ag-note">{t('create.uses.mcp.noMatch', { query: query.trim() })}</p>}
      </div>
    </div>
  )
}
