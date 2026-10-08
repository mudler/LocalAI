import { useEffect, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { agentHref } from '../../utils/libraryText'
import { agentSkills, estimateTokens, passageScore, passageSource, passageText, skillLoadTokens, skillsMode } from '../../utils/library'

// The strip in a pane: who uses this, each name a link to the agent with a
// small x to take the item away, and the Add to... menu at the end.
export function UsedByStrip({ users, status, onRemove, busy, children }) {
  const { t } = useTranslation('library')
  return (
    <div className="lib-usedby" data-testid="used-by">
      <span className="lib-eyebrow">{t('usedBy.label')}</span>
      {status === 'failed' ? (
        <span className="lib-muted">{t('usedBy.unread')}</span>
      ) : status !== 'ready' ? (
        <span className="lib-muted">...</span>
      ) : users.length === 0 ? (
        <span className="lib-muted" data-testid="used-by-none">{t('usedBy.none')}</span>
      ) : (
        <ul className="lib-chips">
          {users.map(u => (
            <li key={u.name} className="dk-chip dk-chip--sm lib-chip" data-testid={`used-by-${u.name}`}>
              <Link className="lib-chip__link" to={agentHref(u.name)} title={t('usedBy.open', { agent: u.name })}>{u.name}</Link>
              {u.all && <span className="lib-chip__note" title={t('usedBy.allTitle')}>{t('usedBy.all')}</span>}
              <button
                type="button"
                className="dk-chip-x"
                aria-label={t('usedBy.remove', { agent: u.name })}
                disabled={busy}
                onClick={() => onRemove(u.name)}
              >
                <Icon name="close" />
              </button>
            </li>
          ))}
        </ul>
      )}
      <span className="lib-usedby__end">{children}</span>
    </div>
  )
}

// The Add to... menu. A skill can go to any agent; a collection can go only to
// the agent that carries its name, because that is the collection an agent
// reads. Each row says what the addition costs where that can be worked out.
export function AddToMenu({ kind, name, skill, agents, allSkillNames, status, onAdd, busy, buttonClass = 'dk-btn dk-btn--secondary dk-btn--sm' }) {
  const { t } = useTranslation('library')
  const [open, setOpen] = useState(false)
  const root = useRef(null)

  useEffect(() => {
    if (!open) return undefined
    const onDown = e => { if (root.current && !root.current.contains(e.target)) setOpen(false) }
    const onKey = e => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const rows = kind === 'collection' ? agents.filter(a => a.name === name) : agents
  const hasAgent = rows.length > 0

  const rowFor = agent => {
    if (kind === 'collection') {
      const on = !!agent.config?.enable_kb
      return { on, hint: on ? '' : t('add.kbOn') }
    }
    const s = agentSkills(agent.config, allSkillNames)
    const on = s.names.includes(name)
    if (on) return { on, hint: '' }
    const tokens = skillLoadTokens(skill, agent.config)
    const hint = skillsMode(agent.config) === 'tools' ? t('add.toolsMode') : t('add.tokens', { count: tokens })
    return { on, hint }
  }

  return (
    <span className="dk-anchor" ref={root}>
      <button
        type="button"
        className={buttonClass}
        aria-haspopup="dialog"
        aria-expanded={open}
        disabled={busy || status === 'loading'}
        onClick={() => setOpen(v => !v)}
        data-testid="add-to-button"
      >
        <Icon name="plus" /> {t('add.button')}
      </button>
      {open && (
        <div className="dk-popover dk-popover--end dk-pop lib-addmenu" role="dialog" aria-label={t('add.title', { name })} data-state="open" data-testid="add-to-menu">
          <p className="lib-addmenu__title">{t('add.title', { name })}</p>
          {kind === 'skill' && hasAgent && <p className="lib-addmenu__sub">{t('add.estimate')}</p>}
          {kind === 'collection' && <p className="lib-addmenu__sub">{t('add.collectionNote')}</p>}
          {status === 'failed' ? (
            <p className="lib-addmenu__empty">{t('usedBy.unread')}</p>
          ) : !hasAgent ? (
            <div className="lib-addmenu__empty">
              <p>{kind === 'collection' ? t('add.noAgentNamed', { name }) : t('add.noAgents')}</p>
              <Link className="dk-link" to="/app/agents/new">{t('add.createAgent')}</Link>
            </div>
          ) : (
            <>
              <span className="lib-eyebrow">{t('add.agents')}</span>
              <ul className="lib-addmenu__list">
                {rows.map(agent => {
                  const { on, hint } = rowFor(agent)
                  return (
                    <li key={agent.name} className="lib-addmenu__row" data-testid={`add-to-${agent.name}`}>
                      <span className="lib-addmenu__main">
                        <span className="lib-addmenu__name">{agent.name}</span>
                        {hint && <span className="lib-addmenu__hint">{hint}</span>}
                      </span>
                      {on ? (
                        <span className="lib-addmenu__done"><Icon name="check" /> {t('add.added')}</span>
                      ) : (
                        <button
                          type="button"
                          className="dk-btn dk-btn--ghost dk-btn--sm"
                          disabled={busy}
                          aria-label={t('add.addToAgent', { agent: agent.name })}
                          onClick={async () => { await onAdd(agent.name); setOpen(false) }}
                        >
                          {t('add.action')}
                        </button>
                      )}
                    </li>
                  )
                })}
              </ul>
            </>
          )}
        </div>
      )}
    </span>
  )
}

// A similarity score as a number and a short bar.
export function ScoreBar({ score, label }) {
  const width = score == null ? 0 : Math.max(0, Math.min(1, score)) * 100
  return (
    <span className="lib-score" aria-label={label} role="img">
      <span className="lib-score__n">{score == null ? '-' : score.toFixed(2)}</span>
      <span className="lib-score__track"><span className="lib-score__fill" style={{ width: `${width}%` }} /></span>
    </span>
  )
}


// Passages a search returned, best first, each with where it came from, an
// estimate of its size and its score.
export function PassageList({ results }) {
  const { t } = useTranslation('library')
  return (
    <ol className="lib-passages" data-testid="passages">
      {results.map((r, i) => {
        const score = passageScore(r)
        const source = passageSource(r)
        const text = passageText(r)
        return (
          <li key={r.id || i} className="lib-passage">
            <span className="lib-passage__n">{i + 1}</span>
            <div className="lib-passage__main">
              <p className="lib-passage__text">{text}</p>
              <p className="lib-passage__meta">
                {source && <span className="lib-mono">{source}</span>}
                <span>{t('simulate.tokens', { count: estimateTokens(text) })}</span>
              </p>
            </div>
            <ScoreBar score={score} label={t('simulate.score', { score: score == null ? '-' : score.toFixed(2) })} />
          </li>
        )
      })}
    </ol>
  )
}
