/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { Link } from 'react-router-dom'
import { Trans, useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { SwitchPanel } from '../chat/SwitchMenu'
import { SwitchRow } from '../chat/SwitchList'
import { addonNow, addonSkillRows } from '../../utils/agentUses'
import { skillKey } from '../../utils/chatSkills'
import './agentuses.css'

// "What it can use": one section, by source. Built-in tools, add-ons (one row
// each: switch, the account it uses, what applies right now, its skills and
// tools, the resources it may use) and MCP servers. It writes fields the form
// has; the sentences come from addonNow, which mirrors the server's choice.
// Every word for an add-on or its resources comes from the row the server
// sends (title, noun, noun_plural); nothing here names one.

// The tools of a row as words: "9 tools (4 read, 5 change data)".
function toolsText(t, v) {
  return v.split ? t('create.uses.toolsSplit', { count: v.count, read: v.read, write: v.write }) : t('create.uses.tools', { count: v.count })
}

function Source({ title, lead, testId, children }) {
  return (
    <section className="au-src" data-testid={testId}>
      <h4 className="au-src__h">{title}</h4>
      <p className="au-src__lead">{lead}</p>
      {children}
    </section>
  )
}

// The word for the resources of an add-on, in the language of its manifest.
function Noun({ language, children }) {
  return <bdi lang={language || undefined}>{children}</bdi>
}

function Accounts({ a, onAccounts }) {
  const { t } = useTranslation('agents')
  const accounts = a.connections || []
  const chosen = a.enabled_connections || []
  const tick = (id, on) => onAccounts(a.id, accounts.map((c) => c.id).filter((x) => (x === id ? on : chosen.includes(x))))
  return (
    <div className="au-field" role="group" aria-label={t('create.uses.account.group', { name: a.title || a.id })} data-testid="agent-addon-accounts">
      <span className="au-field__label">
        {t('create.uses.account.label')}
        {a.off && chosen.length > 0 && <span className="au-tag" data-testid="agent-addon-kept">{t('create.uses.account.kept')}</span>}
      </span>
      {accounts.length === 0 ? (
        <span className="au-field__none">
          {t('create.uses.account.none')}{' '}
          <Link className="swl-pick__link" to="/app/chats/connectors">{t('create.uses.account.add')}</Link>
        </span>
      ) : (
        <span className="au-opts">
          {accounts.map((c) => (
            <label key={c.id} className="dk-choice au-opt" data-testid="agent-addon-account">
              <input type="checkbox" className="dk-check" checked={chosen.includes(c.id)} data-value={c.id} onChange={(e) => tick(c.id, e.target.checked)} />
              <span>{c.name}</span>
            </label>
          ))}
        </span>
      )}
    </div>
  )
}

// The list "<plural noun> it may use", with a read / read and change choice for
// each. An empty list means none.
function Resources({ a, choice, onResource }) {
  const { t } = useTranslation('agents')
  const r = a.resources
  const picked = a.agent_resources || []
  const modeOf = (id) => picked.find((e) => e.id === id)?.mode
  const items = choice?.items || []
  // A ticked resource the list no longer shows (deleted, or no access now).
  // While the list loads nothing is called missing.
  const missing = choice?.status === 'loading' ? [] : picked.filter((e) => !items.some((x) => x.id === e.id))
  const plural = r.noun_plural || r.noun || ''
  const noun = r.noun || plural
  return (
    <div className="au-field" role="group" aria-label={t('create.uses.res.headingPlain', { plural })} data-testid="agent-addon-resources">
      <span className="au-field__label">
        <span><Trans t={t} i18nKey="create.uses.res.heading" values={{ plural }} components={{ n: <Noun language={r.language} /> }} /></span>
      </span>
      {choice?.status === 'loading' && <span className="au-field__none">{t('create.uses.res.loading')}</span>}
      {choice?.status === 'error' && <span className="au-field__none" role="alert" data-testid="agent-addon-resources-error">{t('create.uses.res.error')}</span>}
      {choice?.status === 'ready' && items.length === 0 && missing.length === 0 && (
        <span className="au-field__none" data-testid="agent-addon-resources-empty">
          <Trans t={t} i18nKey="create.uses.res.empty" values={{ plural }} components={{ n: <Noun language={r.language} /> }} />
        </span>
      )}
      {[...items, ...missing.map((e) => ({ id: e.id, name: e.id, gone: true }))].map((x) => {
        const mode = modeOf(x.id)
        return (
          <span key={x.id} className="au-res" data-testid="agent-addon-resource">
            <label className="dk-choice au-opt">
              <input type="checkbox" className="dk-check" checked={Boolean(mode)} onChange={(e) => onResource(a.id, x.id, e.target.checked ? 'read' : null)} />
              <span>{x.name || x.id}</span>
              {x.gone && <span className="au-tag">{t('create.uses.res.gone')}</span>}
            </label>
            <select
              className="au-res__mode" aria-label={t('create.uses.res.access', { name: x.name || x.id })} disabled={!mode}
              value={mode || 'read'} onChange={(e) => onResource(a.id, x.id, e.target.value)} data-testid="agent-addon-resource-mode"
            >
              <option value="read">{t('create.uses.res.read')}</option>
              <option value="write" disabled={x.role === 'viewer'}>{t('create.uses.res.write')}</option>
            </select>
          </span>
        )
      })}
      {choice?.truncated && <span className="au-field__none">{t('create.uses.res.truncated')}</span>}
      <span className="au-field__none" data-testid="agent-addon-resources-help">
        {picked.length
          ? t('create.uses.res.some')
          : <Trans t={t} i18nKey="create.uses.res.none" values={{ noun }} components={{ n: <Noun language={r.language} /> }} />}
      </span>
    </div>
  )
}

function AddonRow({ a, skills, choice, notice, onAddon, onAccounts, onSkill, onResource }) {
  const { t } = useTranslation('agents')
  const hintId = useId()
  const now = addonNow(a, { total: choice?.status === 'ready' ? choice.items.length : null })
  const tl = a.tool_list || []
  const on = skills.filter((s) => s.enabled).length
  const nowText = t(`create.uses.now.${now.key}`, { ...now.vars, tools: toolsText(t, now.vars), noun: now.vars.noun, plural: now.vars.plural, count: now.vars.count })
  const meta = a.credentialed ? t('create.uses.meta.account') : a.resources ? t('create.uses.meta.resources', { plural: a.resources.noun_plural || a.resources.noun || '' }) : undefined
  const extra = (
    <div className="au-body">
      {a.credentialed && <Accounts a={a} onAccounts={onAccounts} />}
      {a.resources && <Resources a={a} choice={choice} onResource={onResource} />}
      {notice}
      <p className="au-now" data-tone={now.tone} data-code={now.code || undefined} id={hintId} role="status" data-testid="agent-addon-now">
        <Icon name={now.tone === 'warn' ? 'warning' : now.tone === 'off' ? 'minus' : 'check'} className="au-now__icon" />
        <span><span className="au-now__k">{t('create.uses.now.label')}</span> {nowText}</span>
      </p>
      {skills.length > 0 && (
        <details className="au-details" data-testid="agent-addon-skills">
          <summary>{a.off ? t('create.uses.skills.summaryOff', { count: skills.length }) : t('create.uses.skills.summaryOn', { on, total: skills.length })}</summary>
          <ul className="au-skills">
            {skills.map((s) => (
              <li key={skillKey(s)}>
                <label className="dk-choice au-opt"><input type="checkbox" className="dk-check" checked={!!s.enabled} onChange={(e) => onSkill(s, e.target.checked)} data-testid="agent-addon-skill" /><span><code>{s.name}</code></span></label>
                {s.description && <span className="au-skills__d">{s.description}</span>}
              </li>
            ))}
          </ul>
        </details>
      )}
      {tl.length > 0 && (
        <details className="au-details" data-testid="agent-addon-tools">
          <summary>{t('create.uses.tools', { count: tl.length })}</summary>
          <ul className="au-tools">
            {tl.map((x) => (
              <li key={x.name}><code>{x.name}</code>{x.kind === 'write' && <span className="au-write">{t('create.uses.writes')}</span>}</li>
            ))}
          </ul>
        </details>
      )}
    </div>
  )
  return (
    <SwitchRow
      name={a.title || a.id}
      blurb={a.description}
      checked={!a.off}
      onChange={(next) => onAddon(a.id, next)}
      extra={extra}
      extraId={hintId}
      meta={meta}
      testId="agent-addon"
      data={{ addon: a.id, tone: now.tone }}
    />
  )
}

// builtin / connector groups are the groups of useToolGroups, split by source.
export default function AgentUses({
  presets, presetNote, notices, builtinGroups, error, network, mcpNode, footer,
  addons, skillRows, choices, appliedFor, onAddon, onAccounts, onSkill, onResource,
}) {
  const { t } = useTranslation('agents')
  return (
    <div className="au" data-testid="agent-uses">
      {presets}
      {presetNote && <p className="ag-note" data-testid="agent-preset-note">{presetNote}</p>}
      {notices}
      <Source title={t('create.uses.builtins.title')} lead={t('create.uses.builtins.lead')} testId="agent-uses-builtins">
        <div className="ag-switches" data-testid="agent-tools-panel">
          <SwitchPanel heading="" groups={builtinGroups} error={error} testId="agent-tools" />
        </div>
        {network}
      </Source>
      <Source title={t('create.uses.addons.title')} lead={t('create.uses.addons.lead')} testId="agent-uses-addons">
        <div className="au-list">
          {addons.length === 0 && <p className="ag-note" data-testid="agent-uses-addons-empty">{t('create.uses.addons.none')}</p>}
          {addons.filter((a) => !a.gone).map((a) => (
            <AddonRow
              key={a.id} a={a} skills={addonSkillRows(skillRows, a.id)} choice={choices[a.id]} notice={appliedFor(a.id)}
              onAddon={onAddon} onAccounts={onAccounts} onSkill={onSkill} onResource={onResource}
            />
          ))}
          {addons.filter((a) => a.gone).map((a) => (
            <SwitchRow key={a.id} name={a.id} desc={t('create.uses.addons.gone')} checked onChange={(next) => onAddon(a.id, next)} testId="agent-addon-gone" />
          ))}
        </div>
      </Source>
      <Source title={t('create.uses.mcp.title')} lead={t('create.uses.mcp.lead')} testId="agent-uses-mcp">
        {mcpNode}
        {footer}
      </Source>
    </div>
  )
}
