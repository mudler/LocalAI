import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { groupConversations } from '../../utils/homeConversations'
import Icon from '../Icon'

// Rows shown before "Show more". Enough to resume yesterday's work without a
// wall of history.
const INITIAL_ROWS = 8
const MAX_ROWS = 60

function isTyping(el) {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

// eslint-disable-next-line no-unused-vars
function EnterKey() {
  return <kbd className="dk-kbd">↵</kbd>
}

// "Jump back in": conversations from this browser, one card per day. A row
// opens its conversation in Chat. j and k move between rows, Enter resumes,
// Delete removes with an undo.
export default function HomeResume({ items, leavingId, onResume, onDelete, emptyHint }) {
  const { t, i18n } = useTranslation('home')
  const [expanded, setExpanded] = useState(false)
  const listRef = useRef(null)

  const visible = useMemo(
    () => items.slice(0, expanded ? MAX_ROWS : INITIAL_ROWS),
    [items, expanded],
  )
  const hidden = Math.min(items.length, MAX_ROWS) - visible.length
  const groups = useMemo(() => groupConversations(visible), [visible])

  const fmtTime = useMemo(() => new Intl.DateTimeFormat(i18n.language, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }), [i18n.language])
  const fmtDay = useMemo(() => new Intl.DateTimeFormat(i18n.language, { weekday: 'short', day: 'numeric', month: 'short' }), [i18n.language])
  const fmtDate = useMemo(() => new Intl.DateTimeFormat(i18n.language, { day: 'numeric', month: 'short' }), [i18n.language])

  // The row is about to go, so focus moves to its neighbour. Keyboard users keep
  // their place in the list.
  const remove = (conv, li) => {
    const rows = Array.from(listRef.current?.querySelectorAll('.home-row__open') || [])
    const mine = li.querySelector('.home-row__open')
    const at = rows.indexOf(mine)
    const next = rows[at + 1] || rows[at - 1]
    if (next && li.contains(document.activeElement)) next.focus()
    onDelete(conv)
  }

  // j and k from anywhere on the page that is not a text field.
  useEffect(() => {
    const onKey = (e) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key !== 'j' && e.key !== 'k') return
      if (isTyping(document.activeElement)) return
      const rows = Array.from(listRef.current?.querySelectorAll('.home-row__open') || [])
      if (!rows.length) return
      e.preventDefault()
      const cur = rows.indexOf(document.activeElement)
      const next = e.key === 'j' ? Math.min(rows.length - 1, cur + 1) : Math.max(0, cur < 0 ? 0 : cur - 1)
      rows[next].focus()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  if (items.length === 0) {
    return (
      <section className="home-resume" aria-labelledby="home-resume-title">
        <div className="home-resume__head"><h2 id="home-resume-title">{t('jump.heading')}</h2></div>
        <div className="home-empty">
          <p><b>{t('jump.emptyTitle')}</b> {t('jump.emptyBody')}</p>
          {emptyHint}
        </div>
      </section>
    )
  }

  return (
    <section className="home-resume" aria-labelledby="home-resume-title">
      <div className="home-resume__head">
        <h2 id="home-resume-title">{t('jump.heading')}</h2>
        <span className="home-resume__keys">
          <kbd className="dk-kbd">j</kbd><kbd className="dk-kbd">k</kbd> {t('jump.move')} <EnterKey /> {t('jump.resume')}
        </span>
      </div>
      <div ref={listRef}>
        {groups.map(g => {
          const first = g.items[0]
          const label = t(`jump.group.${g.key}`)
          const date = g.key === 'today' || g.key === 'yesterday' ? fmtDay.format(first.updatedAt) : ''
          return (
            <div className="home-day" key={g.key} data-testid={`home-day-${g.key}`}>
              <h3 className="home-day__label">{label}{date && <span>{date}</span>}</h3>
              <ul className="home-day__card">
                {g.items.map(c => (
                  <li
                    key={c.id}
                    className={`home-row${leavingId === c.id ? ' home-row--leaving' : ''}`}
                    data-testid="home-conversation"
                    onKeyDown={(e) => {
                      if (e.key === 'Delete' && e.target.classList.contains('home-row__open')) {
                        e.preventDefault()
                        remove(c, e.currentTarget)
                      }
                    }}
                  >
                    <button type="button" className="home-row__open" onClick={() => onResume(c)}>
                      <span className="home-row__time">
                        {g.key === 'older' ? fmtDate.format(c.updatedAt) : fmtTime.format(c.updatedAt)}
                      </span>
                      <span className="home-row__main">
                        <span className="home-row__title">{c.title}</span>
                        {c.preview && (
                          <span className="home-row__reply"><Icon name="corner-down-right" /><span>{c.preview}</span></span>
                        )}
                      </span>
                      <span className="home-row__model">
                        {c.hasImage && <Icon name="eye" title={t('jump.hasImage')} />}
                        {c.model || t('jump.noModel')}
                      </span>
                      <span className="home-row__end">
                        <span className="home-row__count">{t('jump.messages', { count: c.count })}</span>
                        <span className="home-row__resume">{t('jump.resume')} <EnterKey /></span>
                      </span>
                    </button>
                    <button
                      type="button"
                      className="home-row__delete"
                      onClick={(e) => remove(c, e.currentTarget.closest('li'))}
                      title={t('jump.delete')}
                      aria-label={t('jump.deleteNamed', { title: c.title })}
                    >
                      <Icon name="trash" />
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )
        })}
      </div>
      {hidden > 0 && (
        <button type="button" className="home-more" onClick={() => setExpanded(true)}>
          {t('jump.showMore', { count: hidden })}
        </button>
      )}
    </section>
  )
}
