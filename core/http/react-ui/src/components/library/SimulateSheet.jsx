import { useEffect, useMemo, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { agentCollectionsApi } from '../../utils/api'
import {
  agentSkills, estimateTokens, kbResults, kbSearchesEveryMessage, passageText, skillLoadTokens, skillsMode,
} from '../../utils/library'
// eslint-disable-next-line no-unused-vars
import { PassageList } from './LibraryBits'
import { agentHref } from '../../utils/libraryText'

const COLLECTION = '__collection__'

// What a context would load for a message, from the parts the API can run: a
// search on a collection (real passages with their scores) and the skills an
// agent has switched on. It cannot run a model, so it never shows an answer.
// All token numbers are estimates: characters divided by four.
//
//   agents, agentsStatus   from useLibraryFacts
//   skills                 the skill list, with content
//   collections            collection names
//   seed                   { kind: 'skill' | 'collection', name } to try an
//                          item that is not in the chosen context
export default function SimulateSheet(props) {
  if (!props.open) return null
  return <Sheet {...props} />
}

// eslint-disable-next-line no-unused-vars
function Sheet({ onClose, agents, agentsStatus, skills, collections, seed }) {
  const { t } = useTranslation('library')
  const sheetRef = useRef(null)
  const closeRef = useRef(null)
  const openerRef = useRef(null)

  const firstContext = seed?.kind === 'skill' && agents.length > 0 ? agents[0].name
    : (collections.length > 0 || agents.length === 0 ? COLLECTION : agents[0].name)
  const [context, setContext] = useState(firstContext)
  const [collection, setCollection] = useState(seed?.kind === 'collection' ? seed.name : (collections[0] || ''))
  const [message, setMessage] = useState('')
  const [maxResults, setMaxResults] = useState(5)
  // Items tried only in this sheet. They are never saved to an agent.
  const [extras, setExtras] = useState(() => (seed ? [{ kind: seed.kind, name: seed.name }] : []))
  const [state, setState] = useState({ phase: 'idle' })

  useEffect(() => {
    openerRef.current = document.activeElement
    closeRef.current?.focus()
    return () => {
      const opener = openerRef.current
      if (opener && typeof opener.focus === 'function') opener.focus()
    }
  }, [])

  useEffect(() => {
    const onKey = e => {
      if (e.key === 'Escape') { e.stopPropagation(); onClose(); return }
      if (e.key !== 'Tab' || !sheetRef.current) return
      const focusable = sheetRef.current.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled])')
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const allSkillNames = useMemo(() => skills.map(s => s.name), [skills])
  const agent = agents.find(a => a.name === context) || null
  const isCollection = context === COLLECTION

  // Changing the context clears the last result: it described another one.
  const pickContext = value => { setContext(value); setState({ phase: 'idle' }) }

  const extraSkills = extras.filter(e => e.kind === 'skill').map(e => e.name)
  const extraCollections = extras.filter(e => e.kind === 'collection').map(e => e.name)

  const run = async () => {
    const text = message.trim()
    if (!text) { setState({ phase: 'error', message: t('simulate.pickMessage') }); return }
    setState({ phase: 'running' })
    // Which collections to search, and why.
    const targets = []
    if (isCollection) {
      if (collection) targets.push({ name: collection, why: 'alone' })
    } else if (agent) {
      if (kbSearchesEveryMessage(agent.config) && collections.includes(agent.name)) targets.push({ name: agent.name, why: 'agent' })
      for (const name of extraCollections) if (!targets.some(x => x.name === name)) targets.push({ name, why: 'test' })
    }
    const limit = isCollection ? maxResults : (agent ? kbResults(agent.config) : maxResults)
    try {
      const groups = await Promise.all(targets.map(async target => {
        const data = await agentCollectionsApi.search(target.name, text, limit)
        const results = Array.isArray(data?.results) ? data.results : []
        return { ...target, results }
      }))
      setState({ phase: 'done', groups, text })
    } catch (err) {
      setState({ phase: 'error', message: t('simulate.searchFailed', { message: err.message }) })
    }
  }

  // What the agent keeps around a message, estimated.
  const budget = useMemo(() => {
    if (!agent || state.phase !== 'done') return null
    const sk = agentSkills(agent.config, allSkillNames)
    const names = [...sk.names, ...extraSkills.filter(n => !sk.names.includes(n))]
    const loaded = names.map(name => {
      const skill = skills.find(s => s.name === name)
      return { name, missing: !skill, tokens: skill ? skillLoadTokens(skill, agent.config) : 0, test: extraSkills.includes(name) && !sk.names.includes(name) }
    })
    const instructions = estimateTokens(agent.config?.system_prompt || '')
    const skillTokens = loaded.reduce((n, s) => n + s.tokens, 0)
    const memoryTokens = state.groups.reduce((n, g) => n + g.results.reduce((m, r) => m + estimateTokens(passageText(r)), 0), 0)
    const total = instructions + skillTokens + memoryTokens
    return { loaded, instructions, skillTokens, memoryTokens, total, message: estimateTokens(state.text), allOn: sk.all }
  }, [agent, state, skills, allSkillNames, extraSkills])

  const addExtra = item => setExtras(prev => (prev.some(e => e.kind === item.kind && e.name === item.name) ? prev : [...prev, item]))
  const dropExtra = item => { setExtras(prev => prev.filter(e => !(e.kind === item.kind && e.name === item.name))); setState({ phase: 'idle' }) }

  const pct = n => (budget && budget.total > 0 ? Math.round((n / budget.total) * 100) : 0)

  return (
    <div className="dk-sheet-veil lib-veil" data-state="open" onClick={onClose}>
      <div
        ref={sheetRef}
        className="dk-sheet lib-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="simulate-title"
        data-state="open"
        data-testid="simulate-sheet"
        onClick={e => e.stopPropagation()}
      >
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id="simulate-title">{t('simulate.title')}</h2>
            <p className="dk-sheet-desc">{t('simulate.desc')}</p>
          </div>
          <button ref={closeRef} type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('simulate.close')} onClick={onClose}>
            <Icon name="close" />
          </button>
        </div>

        <div className="dk-sheet-body lib-sheet__body">
          <div className="dk-field">
            <label className="dk-label" htmlFor="sim-context">{t('simulate.context')}</label>
            <span className="dk-select-wrap">
              <select id="sim-context" className="dk-select" value={context} onChange={e => pickContext(e.target.value)}>
                <option value={COLLECTION}>{t('simulate.contextCollection')}</option>
                {agents.map(a => <option key={a.name} value={a.name}>{t('simulate.contextAgent', { name: a.name })}</option>)}
              </select>
            </span>
            <p className="dk-hint">{isCollection ? t('simulate.contextHintCollection') : t('simulate.contextHintAgent')}</p>
            {agentsStatus === 'ready' && agents.length === 0 && <p className="dk-hint">{t('simulate.noAgents')}</p>}
          </div>

          {isCollection && (
            <div className="lib-sim-row">
              <div className="dk-field lib-sim-grow">
                <label className="dk-label" htmlFor="sim-collection">{t('simulate.collectionOn')}</label>
                {collections.length === 0 ? (
                  <p className="dk-hint">{t('simulate.noCollections')}</p>
                ) : (
                  <span className="dk-select-wrap">
                    <select id="sim-collection" className="dk-select" value={collection} onChange={e => { setCollection(e.target.value); setState({ phase: 'idle' }) }}>
                      {collections.map(c => <option key={c} value={c}>{c}</option>)}
                    </select>
                  </span>
                )}
              </div>
              <div className="dk-field lib-sim-narrow">
                <label className="dk-label" htmlFor="sim-max">{t('simulate.passages')}</label>
                <input id="sim-max" className="dk-input" type="number" min={1} max={50} value={maxResults} onChange={e => setMaxResults(parseInt(e.target.value, 10) || 5)} />
              </div>
            </div>
          )}

          <div className="dk-field">
            <label className="dk-label" htmlFor="sim-message">{t('simulate.message')}</label>
            <textarea
              id="sim-message"
              className="dk-textarea"
              rows={3}
              value={message}
              placeholder={t('simulate.messagePlaceholder')}
              onChange={e => { setMessage(e.target.value); if (state.phase === 'error') setState({ phase: 'idle' }) }}
            />
          </div>

          {!isCollection && (skills.length > 0 || collections.length > 0) && (
            <div className="dk-field">
              <label className="dk-label" htmlFor="sim-extra">{t('simulate.tryAlso')}</label>
              <span className="dk-select-wrap">
                <select
                  id="sim-extra"
                  className="dk-select"
                  value=""
                  onChange={e => {
                    const [kind, ...rest] = e.target.value.split(':')
                    if (kind) { addExtra({ kind, name: rest.join(':') }); setState({ phase: 'idle' }) }
                  }}
                >
                  <option value="">{t('simulate.tryAlsoPick')}</option>
                  {skills.filter(sk => !extraSkills.includes(sk.name)).length > 0 && (
                    <optgroup label={t('simulate.skills')}>
                      {skills.filter(sk => !extraSkills.includes(sk.name)).map(sk => <option key={sk.name} value={`skill:${sk.name}`}>{sk.name}</option>)}
                    </optgroup>
                  )}
                  {collections.filter(c => !extraCollections.includes(c)).length > 0 && (
                    <optgroup label={t('simulate.memory')}>
                      {collections.filter(c => !extraCollections.includes(c)).map(c => <option key={c} value={`collection:${c}`}>{c}</option>)}
                    </optgroup>
                  )}
                </select>
              </span>
            </div>
          )}

          {extras.length > 0 && (
            <div className="lib-only" data-testid="simulate-extras">
              <span className="lib-eyebrow">{t('simulate.onlyForTest')}</span>
              <ul className="lib-chips">
                {extras.map(e => (
                  <li key={`${e.kind}:${e.name}`} className="dk-chip dk-chip--sm lib-chip">
                    <Icon name={e.kind === 'skill' ? 'sparkles' : 'database'} />
                    <span className="lib-chip__link lib-mono">{e.name}</span>
                    <button type="button" className="dk-chip-x" aria-label={t('simulate.removeExtra', { name: e.name })} onClick={() => dropExtra(e)}>
                      <Icon name="close" />
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          <div className="lib-sim-actions">
            <button type="button" className="dk-btn dk-btn--primary" onClick={run} disabled={state.phase === 'running'} aria-busy={state.phase === 'running' || undefined} data-testid="simulate-run">
              <Icon name={state.phase === 'running' ? 'spinner' : 'play'} spin={state.phase === 'running'} /> {state.phase === 'running' ? t('simulate.running') : t('simulate.run')}
            </button>
            <span className="lib-muted lib-sim-note">{t('simulate.backendNote')}</span>
          </div>

          <div aria-live="polite">
            {state.phase === 'error' && <p className="lib-note lib-note--error" role="alert">{state.message}</p>}
          </div>

          {state.phase === 'done' && (
            <div className="lib-result" data-testid="simulate-result">
              {!isCollection && budget && (
                <section className="lib-sim-block" aria-labelledby="sim-budget-h">
                  <h3 className="lib-eyebrow" id="sim-budget-h">{t('simulate.budgetTitle')}</h3>
                  <div
                    className="dk-meter"
                    role="img"
                    aria-label={`${t('simulate.instructions')} ${budget.instructions}, ${t('simulate.skills')} ${budget.skillTokens}, ${t('simulate.memory')} ${budget.memoryTokens}`}
                  >
                    <span className="dk-meter-seg dk-meter-seg--c" style={{ '--dk-w': `${pct(budget.instructions)}%` }} />
                    <span className="dk-meter-seg dk-meter-seg--b" style={{ '--dk-w': `${pct(budget.skillTokens)}%` }} />
                    <span className="dk-meter-seg" style={{ '--dk-w': `${pct(budget.memoryTokens)}%` }} />
                  </div>
                  <p className="lib-sim-total" data-testid="simulate-total">{t('simulate.budgetTotal', { count: budget.total })}</p>
                  <ul className="dk-meter-legend">
                    <li><span className="dk-swatch dk-swatch--c" />{t('simulate.instructions')} <span className="dk-mono">{budget.instructions}</span></li>
                    <li><span className="dk-swatch dk-swatch--b" />{t('simulate.skills')} <span className="dk-mono">{budget.skillTokens}</span></li>
                    <li><span className="dk-swatch" />{t('simulate.memory')} <span className="dk-mono">{budget.memoryTokens}</span></li>
                  </ul>
                  <p className="lib-muted lib-sim-fine">{t('simulate.budgetMessage', { count: budget.message })}. {t('simulate.budgetNote')}</p>
                </section>
              )}

              {!isCollection && budget && (
                <section className="lib-sim-block" aria-labelledby="sim-skills-h">
                  <h3 className="lib-eyebrow" id="sim-skills-h">{t('simulate.skillsTitle')} <span className="lib-count">{budget.loaded.length}</span></h3>
                  {budget.loaded.length === 0 ? (
                    <p className="lib-muted">{t('simulate.skillsNone')}</p>
                  ) : (
                    <>
                      <p className="lib-muted lib-sim-fine">
                        {budget.allOn && `${t('simulate.skillsAll')} `}
                        {t(`simulate.skills${skillsMode(agent.config) === 'tools' ? 'Tools' : skillsMode(agent.config) === 'both' ? 'Both' : 'Prompt'}`)}
                      </p>
                      <ul className="lib-sim-list">
                        {budget.loaded.map(s => (
                          <li key={s.name} className="lib-sim-item" data-testid={`simulate-skill-${s.name}`}>
                            <span className="lib-mono">{s.name}</span>
                            {s.test && <span className="dk-badge dk-badge--accent">{t('simulate.onlyForTest')}</span>}
                            <span className="lib-sim-item__end">
                              {s.missing ? t('simulate.skillMissing', { name: s.name }) : (skillsMode(agent.config) === 'tools' ? t('simulate.skillTokensTools') : t('simulate.skillTokens', { count: s.tokens }))}
                            </span>
                          </li>
                        ))}
                      </ul>
                    </>
                  )}
                </section>
              )}

              <section className="lib-sim-block" aria-labelledby="sim-memory-h">
                <h3 className="lib-eyebrow" id="sim-memory-h">{t('simulate.memoryTitle')}</h3>
                {!isCollection && agent && !agent.config?.enable_kb && extraCollections.length === 0 && <p className="lib-muted">{t('simulate.memoryNone')}</p>}
                {!isCollection && agent && agent.config?.enable_kb && !collections.includes(agent.name) && (
                  <p className="lib-muted">{t('simulate.memoryNoCollection', { name: agent.name })}</p>
                )}
                {!isCollection && agent && agent.config?.enable_kb && collections.includes(agent.name) && !kbSearchesEveryMessage(agent.config) && (
                  <p className="lib-muted">{t('simulate.memoryTools')}</p>
                )}
                {state.groups.map(group => (
                  <div key={group.name} className="lib-sim-group" data-testid={`simulate-passages-${group.name}`}>
                    <p className="lib-muted lib-sim-fine">
                      {group.why === 'test' ? t('simulate.memoryTesting', { name: group.name }) : t('simulate.memoryFrom', { name: group.name })}
                      {' '}
                      {group.results.length > 0 && <>{'· '}{t('simulate.passagesCount', { count: group.results.length })}</>}
                    </p>
                    {group.results.length === 0 ? (
                      <p className="lib-muted">{t('simulate.noPassages')}</p>
                    ) : (
                      <PassageList results={group.results} />
                    )}
                  </div>
                ))}
              </section>

              {!isCollection && agent && (
                <p className="lib-sim-fine"><Link className="dk-link" to={agentHref(agent.name)}>{agent.name}</Link></p>
              )}
            </div>
          )}

        </div>
      </div>
    </div>
  )
}
