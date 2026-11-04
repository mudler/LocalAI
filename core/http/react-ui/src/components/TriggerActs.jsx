import { useTranslation } from 'react-i18next'
import { useBuiltinGrants } from '../hooks/useBuiltinGrants'
import { actNamed, actRows, actsAfter, actsStatus, actsStatusOf, actCatalog } from '../utils/triggerActs'
import { Callout } from './ui'
import './trigger-acts.css'

// What the runs of a trigger may do with nobody present. Three views of the
// same rules (utils/triggerActs.js):
//   ActsEditor   the checklist of the trigger form, drawn like the grants of an
//                agent: a plain-words line on each, sensitive ones marked
//   ActsSummary  the read only view of a trigger the server returned (details
//                page, proposal card)
//   ReadOnlyTag  a short mark for a list row
// A run that holds no act only reads and reports. The server enforces this at
// each tool call; the interface only shows it.

function Tag({ children }) {
  return <span className="ta-tag">{children}</span>
}

/** One short reason why a run only reads, in plain words. */
function ReadOnlyWhy({ why, t }) {
  return <>{t(`triggerActs.why.${why || 'no_acts'}`)}</>
}

/**
 * The editable list. value is the acts of the form, agentGrants the grants of
 * the chosen agent (null: no agent chosen), writes the file the plan names,
 * lock why the trigger is read only whatever is ticked.
 */
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
export function ActsEditor({ value, onChange, agentGrants, writes = '', lock = '' }) {
  const { t } = useTranslation('chats')
  const catalog = actCatalog(useBuiltinGrants())
  const rows = actRows(catalog, value, agentGrants)
  const status = actsStatus({ acts: value, agentGrants, writes, lock })
  return (
    <div className="ta" data-testid="trigger-acts">
      <p className="dk-hint">{t('triggerActs.help')}</p>
      {agentGrants == null && <p className="dk-hint">{t('triggerActs.noAgent')}</p>}
      <Callout tone={status.readOnly ? 'info' : 'warn'} testId="trigger-acts-status" title={status.readOnly ? t('triggerActs.readOnly') : t('triggerActs.canAct')}>
        {status.readOnly
          ? <ReadOnlyWhy why={status.why} t={t} />
          : t('triggerActs.canActText', { count: status.held.length })}
      </Callout>
      {writes && !lock && <p className="dk-hint" data-testid="trigger-acts-writes">{t('triggerActs.writesNote', { file: writes })}</p>}
      <div className="ag-grants" data-testid="trigger-acts-list">
        {rows.map((g) => (
          <label key={g.name} className="ag-grants__row" data-testid="trigger-act" data-act={g.name} data-blocked={g.blocked || undefined}>
            <input
              type="checkbox"
              checked={g.on}
              disabled={!!lock || (g.blocked && !g.on)}
              onChange={(e) => onChange(actsAfter(value, g.name, e.target.checked))}
            />
            <span className="ag-grants__text">
              <span className="ag-grants__title">
                {g.title}
                {g.sensitive && <Tag>{t('triggerActs.sensitive')}</Tag>}
              </span>
              <span className="ag-note">{g.extra ? t('triggerActs.addonRow', { id: g.name.split(':')[1] }) : g.description}</span>
              {g.blocked && <span className="ag-note" data-testid="trigger-act-blocked">{g.on ? t('triggerActs.unused') : t('triggerActs.blocked')}</span>}
            </span>
          </label>
        ))}
      </div>
    </div>
  )
}

/**
 * The read only view of a trigger as the server returned it. compact leaves out
 * the explanation of each grant (the proposal card is short).
 */
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
export function ActsSummary({ trigger, compact = false }) {
  const { t } = useTranslation('chats')
  const catalog = actCatalog(useBuiltinGrants())
  const status = actsStatusOf(trigger)
  if (status.readOnly || status.held.length === 0) {
    const why = status.why || (status.unused.length > 0 ? 'agent' : 'no_acts')
    return (
      <div className="ta ta--sum" data-testid="trigger-acts-summary" data-read-only="true">
        <b>{t('triggerActs.readOnly')}</b>{' '}<span data-testid="trigger-acts-why"><ReadOnlyWhy why={why} t={t} /></span>
      </div>
    )
  }
  const rows = actNamed(catalog, status.held)
  const unused = actNamed(catalog, status.unused)
  return (
    <div className="ta ta--sum" data-testid="trigger-acts-summary">
      <ul className="ta-list">
        {rows.map((g) => (
          <li key={g.name} data-act={g.name}>
            <b>{g.title}</b>{g.sensitive && <Tag>{t('triggerActs.sensitive')}</Tag>}
            {!compact && g.description && <span className="ag-note ta-desc"> {g.description}</span>}
          </li>
        ))}
      </ul>
      {unused.length > 0 && (
        <p className="ag-note" data-testid="trigger-acts-unused">{t('triggerActs.unusedList', { names: unused.map((g) => g.title).join(', ') })}</p>
      )}
    </div>
  )
}

/** A mark for a list row: nothing for a run that can act, "Read only" for one that cannot. */
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
export function ReadOnlyTag({ trigger }) {
  const { t } = useTranslation('chats')
  const status = actsStatusOf(trigger)
  if (!status.readOnly) return <span data-testid="trigger-acts-tag" data-read-only="false">{t('triggerActs.listCan', { count: status.held.length })}</span>
  return <span data-testid="trigger-acts-tag" data-read-only="true" title={t(`triggerActs.why.${status.why || 'no_acts'}`)}>{t('triggerActs.readOnly')}</span>
}
