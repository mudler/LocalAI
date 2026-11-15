/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { blockedCount } from '../../utils/agentEffective'
import './agentparts.css'

const ICON = { allowed: 'check', asks: 'info', off: 'minus', blocked: 'warning', info: 'info' }

// The Effective view: what the agent can do right now, line by line, from the
// form as it stands. Each line names the section it explains and, when
// something is blocked, the one place that fixes it. Nothing here changes the
// form: a fix on this page only moves the reader to the section.
export default function AgentEffective({ lines, dirty, isEdit, hasConnections, onGoTo }) {
  const { t } = useTranslation('agents')
  const blocked = blockedCount(lines)
  return (
    <div className="aed-eff" data-testid="agent-effective">
      <p className="aed-eff__sum" role="status" data-testid="agent-effective-summary" data-blocked={blocked || undefined}>
        {blocked ? t('create.eff.summaryBlocked', { count: blocked }) : t('create.eff.summaryOk')}
      </p>
      {dirty && <p className="ag-note" data-testid="agent-effective-unsaved">{t(isEdit && hasConnections ? 'create.eff.unsavedConn' : 'create.eff.unsaved')}</p>}
      <ul className="aed-eff__list">
        {lines.map((l) => (
          <li key={l.id} className="aed-eff__row" data-state={l.state} data-line={l.id} data-code={l.code || undefined} data-testid="agent-effective-line">
            <Icon name={ICON[l.state] || 'info'} className="aed-eff__icon" />
            <div className="aed-eff__text">
              <span className={['allowed', 'info'].includes(l.state) ? 'sr-only' : 'aed-eff__state'}>{t(`create.eff.state.${l.state}`)}</span>{' '}
              <span>{t(`create.eff.${l.text}`, { ...l.vars, count: l.vars?.count, defaultValue: l.text })}</span>
              {l.vars?.more > 0 && <span className="aed-eff__more">{' '}{t('create.eff.more', { count: l.vars.more })}</span>}
              {l.code && <code className="aed-eff__code">{l.code}</code>}
            </div>
            <div className="aed-eff__acts">
              {l.fix?.kind === 'link' && <Link className="aed-eff__link" to={l.fix.to} data-testid="agent-effective-fix">{t('create.eff.fixOpen')}</Link>}
              {l.fix?.kind === 'section' && (
                <button type="button" className="aed-eff__link" onClick={() => onGoTo(l.fix.section)} data-testid="agent-effective-fix">{t('create.eff.fixHere')}</button>
              )}
              {!(l.fix?.kind === 'section' && l.fix.section === l.section) && (
                <button type="button" className="aed-eff__link aed-eff__link--quiet" onClick={() => onGoTo(l.section)} data-testid="agent-effective-goto">{t(`create.nav.${l.section}`)}</button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </div>
  )
}
