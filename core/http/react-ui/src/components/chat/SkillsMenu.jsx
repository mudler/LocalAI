/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useId } from 'react'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import SearchField from '../ui/SearchField'
import Switch from '../ui/Switch'
import { BulkActions, SwitchGroup, SwitchHead, SwitchPopover, SwitchRow, SwitchRowStatic, SwitchStatus } from './SwitchList'
import { skillGroups } from '../../utils/chatPlugins'
import { SEARCH_MIN, fallbackNotice, filterSkills, skillCounts, skillKey, skillSource } from '../../utils/chatSkills'

// The composer chip that opens the skills this chat may use: one switch per
// skill (name, one line of description, the full text in the tooltip), the
// count, "Select all" and "Disable all", and a search field from nine skills
// on. While a search is on, the two actions work on the skills shown only.
// The server stores the skills that are off, so a skill added later is on.
// A link at the bottom opens the Skills page, where skills are written.
// Nothing shows when there is no skill.
//
// Each row says where the skill comes from: Built-in, the add-on's title, or
// its layer. A built-in skill that an add-on replaces is listed dimmed, with
// who replaces it and no switch; one line under the title says when a
// built-in skill serves because the add-on's own skill is off or not running.
//
// skills: [{ id, name, description, enabled, layer, addon_title, fallback }]
// replaced: the built-in skills an add-on replaces now [{ name, shadowed_by }]
// onSet(ids: Set, on): switches those skills with one request
// error: the text of a refused change, which the hook has already reverted
// plugins: the plugins of the chat (useChatPlugins): { rows, setOn, error }.
//   Their skills are grouped under "Plugin: <name>", with one switch for the
//   plugin in this chat and one for each skill.
export default function SkillsMenu({ skills, replaced = [], onSet, error, plugins, testId = 'chat-skills' }) {
  const { t } = useTranslation('chat')
  if (skills.length === 0 && !(plugins?.rows || []).length) return null
  // The chip counts what the panel counts: the skills a person can switch.
  // A skill the host grants has no switch and is not in the panel's total.
  const { on } = skillCounts(skills.filter((s) => !s.locked))
  return (
    <SwitchPopover icon="book" label={t('skills.label')} title={t('skills.title')} count={on} testId={testId}>
      <SkillsPanel skills={skills} replaced={replaced} onSet={onSet} error={error || plugins?.error} plugins={plugins} testId={testId} />
    </SwitchPopover>
  )
}

// The body of the skills list, without the chip: the header, the search, one
// row per skill and the status line. The agent form draws it inline.
// `heading` replaces the title of the list; `later` is the note under the header (what a skill added later does).
export function SkillsPanel({ skills, replaced = [], onSet, error, plugins, testId = 'chat-skills', heading, later, manage = true, fromLabel = false }) {
  const { t } = useTranslation('chat')
  const [query, setQuery] = useState('')
  const noteId = useId()

  // A locked row is one the host grants (its source says who): it shows its
  // state and takes no part in the count or the bulk actions.
  const free = skills.filter((s) => !s.locked)
  const { on, total } = skillCounts(free)
  const searchable = total >= SEARCH_MIN
  const q = searchable ? query : ''
  const shown = filterSkills(skills, q)
  const filtering = q.trim() !== ''
  const shownIds = new Set(shown.filter((s) => !s.locked).map(skillKey))
  const allIds = new Set(free.map(skillKey))
  const target = filtering ? shownIds : allIds
  const nothingToEnable = !free.some((s) => target.has(skillKey(s)) && !s.enabled)
  const nothingToDisable = !free.some((s) => target.has(skillKey(s)) && s.enabled)
  const fallback = fallbackNotice(skills)
  // The title of the add-on that replaces a built-in skill, from the add-on
  // skill of the same name in the list.
  const titleOf = (id, name) => skills.find((s) => s.layer === 'addon' && s.addon === id && s.name === name)?.addon_title || id
  const shownReplaced = filtering ? filterSkills(replaced, q) : replaced
  // Plugin skills sit under their plugin; a search keeps a plugin whose
  // name or skills match.
  const { plain, groups } = skillGroups(shown, plugins?.rows || [])
  const lq = q.trim().toLowerCase()
  const shownGroups = filtering ? groups.filter((g) => g.skills.length > 0 || g.name.toLowerCase().includes(lq)) : groups

  return (
    <>
      <SwitchHead
        title={heading || t('skills.heading')}
        summary={t('skills.count', { on, total })}
        summaryTestId={`${testId}-summary`}
        actions={(
          <BulkActions
            testId={testId}
            allLabel={t(filtering ? 'skills.selectShown' : 'skills.selectAll')}
            noneLabel={t(filtering ? 'skills.disableShown' : 'skills.disableAll')}
            allDisabled={nothingToEnable}
            noneDisabled={nothingToDisable}
            onAll={() => onSet(target, true)}
            onNone={() => onSet(target, false)}
          />
        )}
      >
        {searchable && (
          <div><SearchField value={query} onChange={setQuery} label={t('skills.search')} testId={`${testId}-search`} /></div>
        )}
        <p className="swl-head__note" id={noteId}>{later || t('skills.later')}</p>
        {fallback && (
          <p className="swl-head__note swl-head__note--warn" data-testid={`${testId}-fallback`}>
            {t('skills.fallback', { count: fallback.skills.length, skills: fallback.skills.join(', '), name: fallback.addons.join(', ') })}
          </p>
        )}
      </SwitchHead>
      <div className="swl-list" data-testid={`${testId}-list`}>
        {plain.map((s) => <SkillRow key={skillKey(s)} skill={s} onSet={onSet} testId={testId} fromLabel={fromLabel} />)}
        {shownGroups.map((g) => <PluginGroup key={g.id || g.name} group={g} onSet={onSet} onPlugin={plugins?.setOn} testId={testId} />)}
        {shownReplaced.map((s) => (
          <SwitchRowStatic
            key={`replaced:${s.name}`}
            name={s.name}
            desc={t('skills.replacedBy', { name: titleOf(s.shadowed_by, s.name) })}
            title={s.description || s.name}
            testId={`${testId}-replaced`}
          />
        ))}
        {shown.length === 0 && shownGroups.length === 0 && <p className="swl-empty">{t('skills.noMatch', { query: query.trim() })}</p>}
      </div>
      <SwitchStatus error={error} failed={t('skills.failed')} testId={`${testId}-error`} reserve>
        {manage && <Button size="sm" variant="ghost" iconEnd="arrow-right" to="/app/skills" data-testid={`${testId}-manage`}>{t('skills.manage')}</Button>}
      </SwitchStatus>
    </>
  )
}

function SkillRow({ skill, onSet, testId, fromLabel, grouped = false }) {
  const { t } = useTranslation('chat')
  const desc = (skill.description || '').trim()
  const src = skillSource(skill)
  const fromProject = Boolean(skill.fromProject)
  const source = src.kind === 'addon' ? t(fromLabel ? 'skills.source.addonFrom' : 'skills.source.addon', { name: src.name }) : src.kind ? t(`skills.source.${src.kind}`, { defaultValue: '' }) : ''
  return (
    <SwitchRow
      name={skill.name}
      desc={skill.unavailable || (desc ? skill.description : '')}
      meta={grouped ? '' : fromProject ? t('skills.fromProject') : skill.locked && skill.sourceLabel ? skill.sourceLabel : source}
      metaTestId={`${testId}-source`}
      metaKind={skill.locked ? 'provider' : src.kind}
      checked={skill.enabled}
      disabled={Boolean(skill.locked)}
      stacked={Boolean(skill.locked)}
      data={skill.locked ? { locked: 'true', ...(fromProject ? { fromproject: 'true' } : {}) } : undefined}
      onChange={(next) => onSet(new Set([skillKey(skill)]), next)}
      testId={`${testId}-item`}
    />
  )
}

// The skills of one plugin under its name, with the plugin's switch for this
// chat in the heading. A plugin switched off in the chat shows no skill.
function PluginGroup({ group, onSet, onPlugin, testId }) {
  const { t } = useTranslation('chat')
  const on = !group.off
  return (
    <SwitchGroup
      title={t('skills.pluginGroup', { name: group.name })}
      icon="puzzle"
      testId={`${testId}-plugin`}
      intro={on ? (group.skills.length === 0 ? t('skills.pluginNoSkills') : '') : t('skills.pluginOff')}
      actions={onPlugin ? (
        <span className="swl-group__switch">
          <Switch checked={on} onChange={(next) => onPlugin(group.id, next)} label={t('skills.pluginSwitch', { name: group.name })} testId={`${testId}-plugin-switch`} />
        </span>
      ) : null}
    >
      {on && group.skills.map((s) => <SkillRow key={skillKey(s)} skill={s} onSet={onSet} testId={testId} grouped />)}
    </SwitchGroup>
  )
}
