import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { chatTriggersApi } from '../utils/api'
import { localInput, reminderOffsetWords, retimePatch, undoPatch, whenText } from '../utils/watch'
import { Button, Callout, LoadError, SkeletonRows, Tag } from './ui'

// The upcoming reminders of a watch, with one verb per action: Snooze (only
// when due soon), Skip, Change time… and Open. Each action can be undone from
// the line that says what happened, which a screen reader hears too.
export default function WatchReminders({ watch }) {
  const { t, i18n } = useTranslation('chats')
  const [list, setList] = useState(null)
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState('')
  const [editing, setEditing] = useState(null)
  const [done, setDone] = useState(null)
  const [failed, setFailed] = useState('')
  const zone = watch.time_zone || undefined
  const when = (iso) => whenText(iso, zone, new Date(), i18n.language)

  const load = useCallback(async () => {
    try {
      const r = await chatTriggersApi.reminders(watch.id)
      setList(r.reminders || [])
      setError(null)
    } catch (e) {
      setError(e)
    }
  }, [watch.id])
  useEffect(() => { load() }, [load])

  const act = async (r, action, body, say) => {
    if (busy) return // one change at a time: a second submit would race the first
    setBusy(`${r.id}:${action}`)
    setFailed('')
    try {
      const after = await chatTriggersApi.patchReminder(watch.id, r.id, body)
      setDone({ id: r.id, action, before: r, text: say(after) })
      setEditing(null)
    } catch (e) {
      setFailed(e.message)
    } finally {
      await load()
      setBusy('')
    }
  }
  const undo = async () => {
    const { before, action } = done
    setBusy('undo')
    try {
      await chatTriggersApi.patchReminder(watch.id, before.id, undoPatch(before, action))
      setDone({ ...done, text: t('watch.doneUndo'), undone: true })
    } catch (e) {
      setFailed(e.message)
    } finally {
      await load()
      setBusy('')
    }
  }

  if (error) return <LoadError title={t('watch.loadFailed', { message: error.message })} error={error} onRetry={load} />
  if (!list) return <SkeletonRows count={2} />

  return (
    <div className="aj-reminders" data-testid="watch-reminders">
      <span className="sr-only" role="status" aria-live="polite">{done?.text || ''}</span>
      {done && (
        <Callout tone="info" testId="watch-done" actions={!done.undone && (
          <Button size="sm" variant="ghost" icon="undo" busy={busy === 'undo'} disabled={!!busy} onClick={undo}>{t('watch.undo')}</Button>
        )}>{done.text}</Callout>
      )}
      {failed && <p className="dk-field-error" role="alert">{failed}</p>}
      {list.length === 0 ? (
        <p className="ag-note" data-testid="watch-reminders-empty">{t('watch.remindersEmpty')}</p>
      ) : (
        <ul className="aj-reminder-list">
          {list.map((r) => (
            <li key={r.id} className="aj-reminder" data-testid="watch-reminder">
              <div className="aj-reminder__text">
                <span className="aj-name">{r.title}</span>
                <span className="aj-sub__text" data-testid="reminder-due">
                  {r.due ? t('watch.dueLine', { when: when(r.due), offset: reminderOffsetWords(t, r) }) : t('watch.started')}
                </span>
                <span className="ag-muted ag-small">{r.task}</span>
                {r.skipped && <Tag>{t('watch.skipped')}</Tag>}
                {r.snoozed_to && !r.skipped && <Tag>{t('watch.snoozed')}</Tag>}
              </div>
              {editing === r.id ? (
                <form className="aj-acts" onSubmit={(e) => {
                  e.preventDefault()
                  const v = new FormData(e.currentTarget).get('at')
                  act(r, 'retime', retimePatch(v), (after) => t('watch.doneRetime', { when: when(after.due) }))
                }}>
                  <label className="sr-only" htmlFor={`rem-at-${r.id}`}>{t('watch.newTime')}</label>
                  <input id={`rem-at-${r.id}`} name="at" type="datetime-local" className="dk-input" defaultValue={localInput(r.due, zone)} required />
                  <Button size="sm" type="submit" disabled={!!busy} busy={busy === `${r.id}:retime`}>{t('watch.saveTime')}</Button>
                  <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>{t('watch.cancel')}</Button>
                </form>
              ) : (
                <div className="aj-acts">
                  {r.snoozable && (
                    <Button size="sm" variant="secondary" disabled={!!busy} busy={busy === `${r.id}:snooze`}
                      onClick={() => act(r, 'snooze', { snooze_minutes: 5 }, (after) => t('watch.doneSnooze', { when: when(after.due) }))}>{t('watch.snooze')}</Button>
                  )}
                  {r.due && !r.skipped && (
                    <Button size="sm" variant="ghost" disabled={!!busy} busy={busy === `${r.id}:skip`}
                      onClick={() => act(r, 'skip', { skip: true }, () => t('watch.doneSkip'))}>{t('watch.skip')}</Button>
                  )}
                  {r.due && <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => setEditing(r.id)}>{t('watch.changeTime')}</Button>}
                  {r.link && <Button size="sm" variant="ghost" icon="external-link" to={r.link}>{t('watch.open')}</Button>}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
