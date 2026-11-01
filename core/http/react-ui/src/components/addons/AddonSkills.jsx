/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Switch } from '../ui'
import { chatBuiltinToolsApi, settingsApi } from '../../utils/api'
import { skillsOf } from './addonState'

// The skills an add-on ships, in its details: each name, and whether it
// replaces the built-in skill of that name. For an administrator, a skill that
// several add-ons replace has a switch: on, this add-on serves it in the
// chats; off, the next add-on that replaces it does. The choice is the pin of
// Settings > Tools (chats_tool_providers), so both places show the same.
export default function AddonSkills({ app, isAdmin }) {
  const { t } = useTranslation('addons')
  const skills = skillsOf(app)
  const replaces = skills.some(s => s.replaces)
  const [rows, setRows] = useState({})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

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
    const next = on ? app.id : row.candidates.find(id => id !== app.id)
    if (!next) return
    setBusy(name)
    setError('')
    try {
      const current = await settingsApi.get()
      const pins = { ...(current?.chats_tool_providers || {}), [name]: next }
      await settingsApi.save({ chats_tool_providers: pins })
      load()
    } catch (e) {
      setError(e.message || 'error')
    } finally {
      setBusy('')
    }
  }

  return (
    <section className="ad-pane__section" aria-labelledby="ad-pane-skills" data-testid="app-skills">
      <h3 className="ad-section__title" id="ad-pane-skills">{t('skills.title', { count: skills.length })}</h3>
      <ul className="ad-adds">
        {skills.map(s => (
          <SkillItem key={s.name} skill={s} app={app} row={rows[s.name]} busy={busy === s.name} onChoose={choose} />
        ))}
      </ul>
      {error && <p className="ad-card__note" role="alert" data-testid="app-skills-error">{t('skills.saveFailed')}</p>}
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
        {skill.replaces && <span className="ad-quiet"> · {t('skills.replaces', { skill: skill.name })}</span>}
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
