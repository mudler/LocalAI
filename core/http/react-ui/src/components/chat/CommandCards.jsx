/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { tokenCount } from '../../utils/chatSettings'
import './commandcards.css'

// What the slash commands draw in the thread: the markers of the context
// commands (stored in the transcript) and the help and about cards (drawn on
// request, never stored, closed with their button or Esc on the composer).

// The line where the person compacted or cleared the model's context. A calm
// rule across the thread with its label in the middle.
export function ContextMarker({ marker, before = 0, after = 0 }) {
  const { t } = useTranslation('chat')
  const cleared = marker === 'cleared'
  return (
    <div className={`cx-marker cx-marker--${marker}`} role="separator" aria-label={cleared ? t('marker.cleared') : undefined} data-testid={`chat-marker-${marker}`}>
      <span className="cx-marker__label">
        <Icon name={cleared ? 'eraser' : 'minimize'} />
        {cleared
          ? <span>{t('marker.cleared')}</span>
          : <span>{t('marker.compacted', { before: tokenCount(before), after: tokenCount(after) })}</span>}
      </span>
      {cleared && <span className="cx-marker__hint">{t('marker.clearedHint')}</span>}
    </div>
  )
}

// The chip on a message sent with a skill or a saved agent.
export function InvokedChip({ invoked }) {
  const { t } = useTranslation('chat')
  if (!invoked?.name) return null
  const agent = invoked.kind === 'agent'
  return (
    <span className="cx-invoked" data-testid="message-invoked">
      <Icon name={agent ? 'robot' : 'book'} />
      <span>{t(agent ? 'invoked.agent' : 'invoked.skill', { name: invoked.name })}</span>
    </span>
  )
}

function CardHead({ icon, title, onClose }) {
  const { t } = useTranslation('chat')
  return (
    <div className="cx-cmdcard__head">
      <Icon name={icon} />
      <h3 className="cx-cmdcard__title">{title}</h3>
      <button type="button" className="cx-icobtn" onClick={onClose} title={t('cmdcard.close')} aria-label={t('cmdcard.close')} data-testid="chat-cmdcard-close">
        <Icon name="close" />
      </button>
    </div>
  )
}

// /help: every command the box offers now, one line each.
//   entries  [{ cmd, desc, icon }]; skills: the names of the skill commands
export function SlashHelpCard({ entries = [], skills = [], onClose }) {
  const { t } = useTranslation('chat')
  return (
    <section className="cx-cmdcard" data-testid="chat-help-card" aria-label={t('cmdcard.helpTitle')}>
      <CardHead icon="help-circle" title={t('cmdcard.helpTitle')} onClose={onClose} />
      <ul className="cx-cmdcard__list">
        {entries.map((e) => (
          <li key={e.cmd} className="cx-cmdcard__row">
            <code className="cx-cmdcard__cmd">{e.cmd}</code>
            <span className="cx-cmdcard__desc">{e.desc}</span>
          </li>
        ))}
      </ul>
      {skills.length > 0 && <p className="cx-cmdcard__foot">{t('cmdcard.helpSkills', { count: skills.length, names: skills.slice(0, 8).join(', ') })}</p>}
      <p className="cx-cmdcard__foot">{t('cmdcard.helpKeys')}</p>
    </section>
  )
}

// /about: what this chat runs with, from what the page already knows.
//   about  { model, warm, backend, tools, skills, commands ('isolated' |
//            'host' | 'none' | ''), used, limit }
export function ChatAboutCard({ about, onClose }) {
  const { t } = useTranslation('chat')
  const rows = [
    [t('cmdcard.model'), about.model || t('cmdcard.unknown')],
    [t('cmdcard.state'), about.warm ? t('cmdcard.loaded') : t('cmdcard.notLoaded')],
  ]
  if (about.backend) rows.push([t('cmdcard.backend'), about.backend])
  rows.push([t('cmdcard.tools'), t('cmdcard.toolsOn', { count: about.tools || 0 })])
  rows.push([t('cmdcard.skills'), t('cmdcard.skillsOn', { count: about.skills || 0 })])
  rows.push([t('cmdcard.commands'), about.commands === 'isolated' ? t('cmdcard.isolated') : about.commands === 'host' ? t('cmdcard.host') : t('cmdcard.noCommands')])
  rows.push([t('cmdcard.context'), about.limit > 0
    ? t('cmdcard.contextOf', { used: tokenCount(about.used || 0), limit: tokenCount(about.limit) })
    : (about.used > 0 ? t('cmdcard.contextUsed', { used: tokenCount(about.used) }) : t('cmdcard.unknown'))])
  return (
    <section className="cx-cmdcard" data-testid="chat-about-card" aria-label={t('cmdcard.aboutTitle')}>
      <CardHead icon="info" title={t('cmdcard.aboutTitle')} onClose={onClose} />
      <dl className="cx-cmdcard__facts">
        {rows.map(([k, v]) => (
          <div key={k} className="cx-cmdcard__fact">
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}
