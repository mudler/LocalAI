/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Switch } from '../ui'
import { chatBuiltinToolsApi } from '../../utils/api'
import { createHostSettingsClient } from '../../utils/hostSettingsClient'
import { createAddonsApi } from '../../utils/addonsApi'
import { apiUrl } from '../../utils/basePath'
import { useHostSettings } from '../../hooks/useHostSettings'
import { skillDeclarationLabel } from '../../utils/chatSkills'
import { skillsOf } from './addonState'

// The skills an add-on ships, in its details: each name, and whether it
// provides a document slot or replaces a remaining built-in. For an administrator, a skill that
// several add-ons replace has a switch: on, this add-on serves it in the
// chats; off, the next add-on that replaces it does. The choice is the pin of
// Settings > Tools (chats_tool_providers), so both places show the same.
const ownerClient = createHostSettingsClient({ api: createAddonsApi({ base: apiUrl }) })

export default function AddonSkills({ app, isAdmin }) {
  const owner = useHostSettings(ownerClient, app.id)
  const { t } = useTranslation('addons')
  const skills = skillsOf(app)
  const replaces = skills.some(s => s.replaces)
  const [rows, setRows] = useState({})

  const load = useCallback(() => {
    if (!isAdmin || !replaces) return () => {}
    let live = true
    chatBuiltinToolsApi.list()
      .then(res => { if (live) setRows(Object.fromEntries((res?.skills || []).map(r => [r.id, r]))) })
      .catch(() => { if (live) setRows({}) })
    return () => { live = false }
  }, [isAdmin, replaces])
  useEffect(() => load(), [load, app.id])

  if (skills.length === 0) return null

  const choose = async (name, on) => {
    const row = rows[name]
    if (!row) return
    const pins = { ...owner.draft.chats_tool_providers }
    if (on) pins[name] = app.id
    else delete pins[name]
    owner.setDraft({ ...owner.draft, chats_tool_providers: pins })
    await owner.save({ chats_tool_providers: { [name]: on ? app.id : null } })
  }

  return (
    <section className="ad-pane__section" aria-labelledby="ad-pane-skills" data-testid="app-skills">
      <h3 className="ad-section__title" id="ad-pane-skills">{t('skills.title', { count: skills.length })}</h3>
      <ul className="ad-adds">
        {skills.map(s => (
          <SkillItem key={s.name} skill={s} app={app} row={rows[s.name] && owner.draft ? { ...rows[s.name], provider: owner.draft.chats_tool_providers[s.name] || rows[s.name].default || rows[s.name].candidates[0] } : undefined} busy={!isAdmin || owner.busy || !!owner.error || !owner.snapshot} onChoose={choose} />
        ))}
      </ul>
      {owner.snapshot && <p>Owner policy · revision {owner.snapshot.revision}</p>}
      {owner.error && <div role="alert" data-testid="app-skills-error">
        {owner.uncertain ? 'Write outcome is uncertain. ' : 'Owner settings unavailable. '}{owner.error.message}
        <button type="button" onClick={owner.reload} disabled={owner.busy}>Reload and rebase edits</button>
        {owner.uncertain && <button type="button" onClick={() => owner.save({})} disabled={owner.busy}>Retry same operation</button>}
      </div>}
      {owner.snapshot && !owner.error && JSON.stringify(owner.draft.chats_tool_providers) !== JSON.stringify(owner.snapshot.policy.chats_tool_providers) && <button type="button" onClick={() => {
        const before = owner.snapshot.policy.chats_tool_providers, after = owner.draft.chats_tool_providers
        const patch = Object.fromEntries([...new Set([...Object.keys(before), ...Object.keys(after)])].filter(k => before[k] !== after[k]).map(k => [k, after[k] ?? null]))
        return owner.save({ chats_tool_providers: patch })
      }} disabled={owner.busy}>Apply</button>}
    </section>
  )
}

function SkillItem({ skill, app, row, busy, onChoose }) {
  const { t } = useTranslation('addons')
  const labelId = useId()
  const serves = row ? row.provider === app.id : false
  const several = row && row.candidates.length > 1
  return (
    <li data-testid={`app-skill-${skill.name}`} className="ad-skill">
      <Icon name="book" aria-hidden="true" />
      <span className="ad-skill__text">
        <span className="dk-mono">{skill.name}</span>
        {skill.replaces && <span className="ad-quiet"> · {t(`skills.${skillDeclarationLabel(skill)}`, { skill: skill.name })}</span>}
        {row && !several && <span className="ad-quiet"> · {serves ? t('skills.serves') : t('skills.notServing')}</span>}
      </span>
      {several && (
        <span className="ad-skill__switch">
          <Switch checked={serves} disabled={busy} labelledBy={labelId} onChange={(on) => onChoose(skill.name, on)} testId={`app-skill-${skill.name}-switch`} />
          <span id={labelId} className="ad-quiet">{t('skills.serveIn', { skill: skill.name })}</span>
        </span>
      )}
    </li>
  )
}
