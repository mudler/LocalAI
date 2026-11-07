import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import {
  standingOf, senderName, threadView, noteCode, noteTone, placeView, peopleOf,
} from '../../utils/connectorChat'

// What a chat that a messaging connection started shows beyond the words. The
// server stored all of it with the chat; nothing here asks the platform.

// The small mark of how the host saw the sender. The words are the same on
// every platform: owner is paired with the bot, verified holds a role the
// owner gave something to, listed is on the allowlist, channel was let in with
// the room it wrote in, anyone was let in because the connection accepts all.
export function StandingBadge({ standing }) {
  const { t } = useTranslation('chat')
  return (
    <span className="cx-standing" data-testid="sender-standing" data-standing={standing} title={t(`connector.standingHelp.${standing}`)}>
      {t(`connector.standing.${standing}`)}
    </span>
  )
}

// The line over a message of a person on the platform: the name, how the host
// saw them and the time. The id shows in small print when it is all there is.
export function SenderLine({ sender, at }) {
  const { t, i18n } = useTranslation('chat')
  const who = senderName(sender, t)
  const time = at ? new Date(at) : null
  return (
    <div className="cx-sender" data-testid="message-sender">
      <b className="cx-sender__name" data-resolved={who.resolved || undefined}>{who.name}</b>
      {who.small && <span className="cx-sender__small" data-testid="sender-small">{who.small}</span>}
      <StandingBadge standing={standingOf(sender)} />
      {time && !Number.isNaN(time.getTime()) && (
        <time className="cx-sender__time" dateTime={time.toISOString()} title={time.toLocaleString(i18n.language)}>
          {time.toLocaleTimeString(i18n.language, { hour: '2-digit', minute: '2-digit' })}
        </time>
      )}
    </div>
  )
}

// The header of a connector chat: which connection, where on the platform, a
// link to the thread when the connection could build one, and who took part.
export function ConnectorHeader({ title, kindTitle, place, messages, href, hrefLabel }) {
  const { t } = useTranslation('chat')
  const p = placeView(place)
  const people = peopleOf(messages)
  const where = p.direct
    ? t('connector.where.direct')
    : p.name ? t('connector.where.channel', { name: p.name }) : t('connector.where.group')
  return (
    <section className="cx-conn" data-testid="chat-origin-banner" aria-label={t('connector.header')}>
      <div className="cx-conn__row">
        <Icon name="chat" className="cx-conn__icon" />
        <span className="cx-conn__title" data-testid="connector-name">{title}</span>
        {kindTitle && kindTitle !== title && <span className="cx-conn__kind" data-testid="connector-kind">{kindTitle}</span>}
        <span className="cx-conn__where" data-testid="connector-where">{where}</span>
        {p.thread && <span className="cx-conn__chip" data-testid="connector-thread">{t('connector.inThread')}</span>}
        <span className="cx-conn__links">
          {p.link && (
            <a className="cx-link" href={p.link} target="_blank" rel="noopener noreferrer" data-testid="connector-permalink">
              {t('connector.openThread')} <Icon name="external-link" />
            </a>
          )}
          {href && <a className="cx-link" href={href} data-testid="chat-origin-link">{hrefLabel}</a>}
        </span>
      </div>
      {people.length > 0 && (
        <ul className="cx-conn__people" data-testid="connector-people" aria-label={t('connector.people')}>
          {people.map((x) => {
            const who = senderName(x.sender, t)
            return (
              <li key={x.key} className="cx-conn__person" data-testid="connector-person">
                <span className="cx-conn__pname">{who.name}</span>
                <StandingBadge standing={x.standing} />
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

// The earlier messages of the thread, where the bot was first addressed: shut,
// with how many. Opened it shows the lines the model read, author and text,
// as they were given to it (same wording, same cut).
export function ThreadContext({ msg }) {
  const { t } = useTranslation('chat')
  const [open, setOpen] = useState(false)
  const id = useId()
  const v = threadView(msg)
  if (v.count === 0) return null
  return (
    <div className="cx-thread-ctx" data-testid="thread-context" data-open={open || undefined}>
      <button
        type="button"
        className="cx-thread-ctx__head"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((o) => !o)}
        data-testid="thread-context-toggle"
      >
        <Icon name={open ? 'chevron-down' : 'chevron-right'} className="cx-thread-ctx__chev" />
        <span>{t('connector.threadEarlier', { count: v.count })}</span>
      </button>
      <ol id={id} className="cx-thread-ctx__lines" hidden={!open} data-testid="thread-context-lines">
        {v.lines.map((l, i) => (
          <li key={i} className="cx-thread-ctx__line">
            {l.author && <b className="cx-thread-ctx__author">{l.author}</b>}
            <span className="cx-thread-ctx__text">{l.text}</span>
          </li>
        ))}
        {v.cut && <li className="cx-thread-ctx__cut" data-testid="thread-context-cut">{t('connector.threadCut')}</li>}
      </ol>
    </div>
  )
}

// A small status line for something the host did on its own about a message:
// a refusal with its reason, the tools held back from a sender, a pairing that
// worked. The sentence the sender was told shows when there is one.
export function HostNote({ msg, count = 1 }) {
  const { t } = useTranslation('chat')
  const code = msg.host_note?.code
  const key = noteCode(code)
  const detail = (msg.text || '').trim()
  const heldBack = key === 'held_back'
  return (
    <div className="cx-hostnote" data-testid="host-note" data-code={code || undefined} data-tone={noteTone(code)}>
      <Icon name={noteTone(code) === 'warn' ? 'warning' : noteTone(code) === 'ok' ? 'check-circle' : 'info'} className="cx-hostnote__icon" />
      <span className="cx-hostnote__label">{t(`connector.note.${key}`)}</span>
      {detail && !heldBack && <span className="cx-hostnote__text" data-testid="host-note-text">{detail}</span>}
      {heldBack && detail && (
        <details className="cx-hostnote__more">
          <summary>{t('connector.note.why')}</summary>
          <span data-testid="host-note-text">{detail}</span>
        </details>
      )}
      {count > 1 && <span className="cx-hostnote__count">×{count}</span>}
    </div>
  )
}
