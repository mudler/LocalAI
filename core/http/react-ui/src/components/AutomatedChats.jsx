/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { StatusChip } from './ui'
import { chatStatus, groupByOrigin, knownKind } from '../utils/originChats'

// How many chats a group lists before "Show all".
const SHOWN = 5

// The chats that something other than a person started, for the second tab of
// All chats. One block per origin kind (triggers, connectors, jobs, pages and
// whatever a later release adds), and inside it one group per origin: the
// binding of a connector, a trigger, a job, a page. A group says how many
// chats it has, when it was last active, whether a turn runs and whether a
// chat waits for the person, and links to the page of its origin.
export default function AutomatedChats({ chats, ctx, needsYou, onOpen, onDelete }) {
  const { t, i18n } = useTranslation('chat')
  const [open, setOpen] = useState({})
  const kinds = useMemo(() => groupByOrigin(chats, ctx, needsYou), [chats, ctx, needsYou])
  const fmt = useMemo(() => new Intl.DateTimeFormat(i18n.language, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }), [i18n.language])

  if (kinds.length === 0) {
    return <div className="ac-empty" data-testid="automated-empty">{t('allChats.automated.empty')}</div>
  }

  return (
    <div className="ac" data-testid="automated-chats">
      <p className="ac-intro">{t('allChats.automated.intro')}</p>
      {kinds.map((k) => {
        const kind = knownKind(k.kind)
        return (
          <section key={k.kind} className="ac-kind" data-testid={`automated-kind-${k.kind}`} aria-labelledby={`ac-kind-${k.kind}`}>
            <h2 id={`ac-kind-${k.kind}`} className="ac-kind__title">
              {t(`allChats.automated.kind.${kind}`)}
              <span className="ac-kind__count">{k.count}</span>
            </h2>
            {k.groups.map((g) => {
              const expanded = Boolean(open[g.key])
              const shown = expanded ? g.chats : g.chats.slice(0, SHOWN)
              const title = g.title || t(`allChats.automated.badge.${kind}`)
              return (
                <div key={g.key} className="ac-group" data-testid="automated-group">
                  <div className="ac-group__head">
                    <div className="ac-group__main">
                      <span className="ac-badge" data-testid="automated-origin-badge" title={t(`menu.origin.${kind}`)}>{t(`allChats.automated.badge.${kind}`)}</span>
                      <h3 className="ac-group__title" data-testid="automated-group-title">{title}</h3>
                    </div>
                    <div className="ac-group__meta">
                      <span data-testid="automated-count">{t('allChats.automated.chats', { count: g.count })}</span>
                      <span>{t('allChats.automated.lastActive', { when: fmt.format(g.lastActive) })}</span>
                      {g.running > 0 && <StatusChip status="starting" label={t('allChats.automated.running')} testId="automated-running" />}
                      {g.waiting > 0 && <StatusChip tone="warn" icon="warning" label={t('allChats.automated.waiting')} testId="automated-waiting" />}
                      {g.href && <Link className="ac-link" to={g.href} data-testid="automated-origin-link">{t('allChats.automated.open', { title })}</Link>}
                    </div>
                  </div>
                  <ul className="ac-list">
                    {shown.map((c) => {
                      const status = chatStatus(c, needsYou)
                      return (
                        <li key={c.id} className="ac-row" data-testid="automated-chat" data-status={status}>
                          <button type="button" className="ac-row__open" onClick={() => onOpen(c.id)}>
                            <span className="ac-row__name">{c.name || t('menu.untitled')}</span>
                            <span className="ac-row__time">{fmt.format(c.updatedAt || c.createdAt || 0)}</span>
                          </button>
                          {status === 'running' && <Icon name="spinner" spin className="ac-row__busy" title={t('allChats.automated.running')} />}
                          {status === 'waiting' && <Icon name="warning" className="ac-row__wait" title={t('allChats.automated.waiting')} />}
                          <button
                            type="button"
                            className="cx-icobtn cx-icobtn--danger"
                            onClick={() => onDelete(c)}
                            title={t('menu.deleteChat')}
                            aria-label={t('menu.deleteChat')}
                          >
                            <Icon name="trash" />
                          </button>
                        </li>
                      )
                    })}
                  </ul>
                  {g.chats.length > SHOWN && (
                    <button type="button" className="cx-link ac-more" onClick={() => setOpen((o) => ({ ...o, [g.key]: !expanded }))}>
                      {expanded ? t('allChats.automated.showLess') : t('allChats.automated.showAll', { count: g.chats.length })}
                    </button>
                  )}
                </div>
              )
            })}
          </section>
        )
      })}
    </div>
  )
}
