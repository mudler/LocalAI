import { useTranslation } from 'react-i18next'
import { Callout } from './ui'
import { OFFSET_CHOICES, offsetWords } from '../utils/watch'

// The Watch section of the trigger form: which add-on to watch, which of its
// changes, and the reminder rule. It takes the place of the schedule section
// for a watch. Only add-ons that report their changes are offered; with none,
// the section says how to get one.
export default function WatchFields({ watch, zone, sources, onChange, problem }) {
  const { t } = useTranslation('chats')
  const set = (field, value) => onChange({ ...watch, [field]: value })
  if (sources && sources.length === 0) {
    return (
      <Callout tone="info" title={t('watch.emptyTitle')} testId="watch-empty">
        {t('watch.emptyBody')}
      </Callout>
    )
  }
  const source = (sources || []).find((s) => s.id === watch.source)
  const offsetLabel = (m) => offsetWords(t, m)
  const toggleType = (type) => {
    const has = watch.types.includes(type)
    set('types', has ? watch.types.filter((x) => x !== type) : [...watch.types, type])
  }
  return (
    <div className="aj-form" data-testid="watch-fields">
      <div className="dk-field">
        <label className="dk-label" htmlFor="aj-watch-source">{t('watch.source')}</label>
        <span className="dk-select-wrap">
          <select id="aj-watch-source" className="dk-select" value={watch.source} onChange={(e) => onChange({ ...watch, source: e.target.value, types: [] })} aria-describedby="aj-watch-source-h">
            <option value="">{t('watch.sourcePick')}</option>
            {(sources || []).map((s) => <option key={s.id} value={s.id}>{s.title}</option>)}
          </select>
        </span>
        <p className="dk-hint" id="aj-watch-source-h">{t('watch.sourceHelp')}</p>
        {problem === 'source' && <p className="dk-field-error" role="alert">{t('watch.problemSource')}</p>}
      </div>
      {source && source.events.length > 0 && (
        <fieldset className="dk-field">
          <legend className="dk-label">{t('watch.types')}</legend>
          {source.events.map((ev) => (
            <label key={ev.type} className="aj-check">
              <input type="checkbox" className="dk-check" checked={watch.types.includes(ev.type)} onChange={() => toggleType(ev.type)} />
              {ev.label}
            </label>
          ))}
          <p className="dk-hint">{t('watch.typesHelp')}</p>
        </fieldset>
      )}
      <div className="dk-field">
        <label className="dk-label" htmlFor="aj-watch-offset">{t('watch.offset')}</label>
        <span className="dk-select-wrap">
          <select id="aj-watch-offset" className="dk-select" value={watch.offset} onChange={(e) => set('offset', e.target.value)}>
            {OFFSET_CHOICES.map((m) => <option key={m} value={String(m)}>{offsetLabel(m)}</option>)}
            {!OFFSET_CHOICES.map(String).includes(watch.offset) && watch.offset !== 'none' && (
              <option value={watch.offset}>{offsetLabel(Number(watch.offset))}</option>
            )}
            <option value="none">{t('watch.none')}</option>
          </select>
        </span>
      </div>
      <div className="dk-field">
        <label className="dk-label" htmlFor="aj-watch-allday">{t('watch.allDay')}</label>
        <span className="dk-select-wrap">
          <select id="aj-watch-allday" className="dk-select" value={watch.allDay} onChange={(e) => set('allDay', e.target.value)}>
            <option value="morning">{t('watch.allDayMorning')}</option>
            <option value="none">{t('watch.allDayNone')}</option>
          </select>
        </span>
      </div>
      <fieldset className="dk-field">
        <legend className="dk-label">{t('watch.quiet')}</legend>
        <div className="aj-row">
          <label className="dk-label" htmlFor="aj-watch-qfrom">{t('watch.quietFrom')}</label>
          <input id="aj-watch-qfrom" type="time" className="dk-input" value={watch.quietFrom} onChange={(e) => set('quietFrom', e.target.value)} />
          <label className="dk-label" htmlFor="aj-watch-qto">{t('watch.quietTo')}</label>
          <input id="aj-watch-qto" type="time" className="dk-input" value={watch.quietTo} onChange={(e) => set('quietTo', e.target.value)} />
        </div>
        <p className="dk-hint">{t('watch.quietHelp')} {zone}</p>
        {problem === 'quiet' && <p className="dk-field-error" role="alert">{t('watch.problemQuiet')}</p>}
      </fieldset>
      <div className="dk-field">
        <label className="dk-label" htmlFor="aj-watch-tasks">{t('watch.tasks')}</label>
        <textarea id="aj-watch-tasks" className="dk-textarea" rows={3} maxLength={1000} value={watch.tasks} onChange={(e) => set('tasks', e.target.value)} aria-describedby="aj-watch-tasks-h" />
        <p className="dk-hint" id="aj-watch-tasks-h">{t('watch.tasksHelp')}</p>
      </div>
    </div>
  )
}
