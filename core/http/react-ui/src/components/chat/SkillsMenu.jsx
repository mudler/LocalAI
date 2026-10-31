/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useId } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { BulkActions, SwitchHead, SwitchPopover, SwitchRow, SwitchRowStatic, SwitchStatus } from './SwitchList'
import { SEARCH_MIN, fallbackNotice, filterSkills, skillCounts, skillKey, skillSource } from '../../utils/chatSkills'

// The composer chip that opens the skills this chat may use: one switch per
// skill (name, one line of description, the full text in the tooltip), the
// count, "Select all" and "Disable all", and a search field from nine skills
// on. While a search is on, the two actions work on the skills shown only.
// The server stores the skills that are off, so a skill added later is on.
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
export default function SkillsMenu({ skills, replaced = [], onSet, error, testId = 'chat-skills' }) {
  const { t } = useTranslation('chat')
  const [query, setQuery] = useState('')
  const noteId = useId()

  if (skills.length === 0) return null
  const { on, total } = skillCounts(skills)
  const searchable = total >= SEARCH_MIN
  const q = searchable ? query : ''
  const shown = filterSkills(skills, q)
  const filtering = q.trim() !== ''
  const shownIds = new Set(shown.map(skillKey))
  const allIds = new Set(skills.map(skillKey))
  const target = filtering ? shownIds : allIds
  const nothingToEnable = !skills.some((s) => target.has(skillKey(s)) && !s.enabled)
  const nothingToDisable = !skills.some((s) => target.has(skillKey(s)) && s.enabled)
  const fallback = fallbackNotice(skills)
  // The title of the add-on that replaces a built-in skill, from the add-on
  // skill of the same name in the list.
  const titleOf = (id, name) => skills.find((s) => s.layer === 'addon' && s.addon === id && s.name === name)?.addon_title || id
  const shownReplaced = filtering ? filterSkills(replaced, q) : replaced

  return (
    <SwitchPopover icon="book" label={t('skills.label')} title={t('skills.title')} count={on} testId={testId}>
      <SwitchHead
        title={t('skills.heading')}
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
          <div className="swl-search">
            <Icon name="search" className="swl-search__icon" />
            <input
              type="search"
              className="dk-input swl-search__input"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('skills.search')}
              aria-label={t('skills.search')}
              data-testid={`${testId}-search`}
            />
          </div>
        )}
        <p className="swl-head__note" id={noteId}>{t('skills.later')}</p>
        {fallback && (
          <p className="swl-head__note swl-head__note--warn" data-testid={`${testId}-fallback`}>
            {t('skills.fallback', { count: fallback.skills.length, skills: fallback.skills.join(', '), name: fallback.addons.join(', ') })}
          </p>
        )}
      </SwitchHead>
      <div className="swl-list" data-testid={`${testId}-list`}>
        {shown.map((s) => <SkillRow key={skillKey(s)} skill={s} onSet={onSet} testId={testId} />)}
        {shownReplaced.map((s) => (
          <SwitchRowStatic
            key={`replaced:${s.name}`}
            name={s.name}
            desc={t('skills.replacedBy', { name: titleOf(s.shadowed_by, s.name) })}
            title={s.description || s.name}
            testId={`${testId}-replaced`}
          />
        ))}
        {shown.length === 0 && <p className="swl-empty">{t('skills.noMatch', { query: query.trim() })}</p>}
      </div>
      <SwitchStatus error={error} failed={t('skills.failed')} testId={`${testId}-error`} />
    </SwitchPopover>
  )
}

function SkillRow({ skill, onSet, testId }) {
  const { t } = useTranslation('chat')
  const desc = (skill.description || '').trim()
  const src = skillSource(skill)
  const source = src.kind === 'addon' ? t('skills.source.addon', { name: src.name }) : src.kind ? t(`skills.source.${src.kind}`, { defaultValue: '' }) : ''
  return (
    <SwitchRow
      name={skill.name}
      desc={desc ? skill.description : ''}
      meta={source}
      metaTestId={`${testId}-source`}
      metaKind={src.kind}
      checked={skill.enabled}
      onChange={(next) => onSet(new Set([skillKey(skill)]), next)}
      testId={`${testId}-item`}
    />
  )
}
