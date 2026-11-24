/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { useWellKnown } from '../../hooks/useWellKnown'
import { usePages, refreshPages } from '../../hooks/usePages'
import { useSeen } from '../../hooks/useSeen'
import { chatMCPConnectorsApi, pagesApi } from '../../utils/api'
import {
  exampleCards, exampleResults, experienced, examplesHidden, hideExamples, showExamples, mailConnector, today, DOCS_MAKE_YOURS,
} from '../../utils/discovery'
import { showChange } from './changeNote'
import './wellKnown.css'

const READY = [
  { id: 'task-board', icon: 'checklist', key: 'readyTasks' },
  { id: 'reading-list', icon: 'book', key: 'readyReading' },
]

// The state the examples need: the well-known files, the pages, and whether a
// mail connector exists. ready is false until every read answered once.
function useExampleState(enabled, { connectors = true } = {}) {
  const persona = useWellKnown('persona', { enabled })
  const shortcuts = useWellKnown('shortcuts', { enabled })
  const home = useWellKnown('home', { enabled })
  const ui = useWellKnown('ui', { enabled })
  const { seen, doc: user, loaded: userLoaded, update } = useSeen({ enabled })
  const pages = usePages(enabled)
  const [mail, setMail] = useState(false)
  useEffect(() => {
    if (!enabled || !connectors) return undefined
    let live = true
    chatMCPConnectorsApi.list()
      .then((r) => { if (live) setMail(mailConnector(r?.connectors || r)) }, () => {})
    return () => { live = false }
  }, [enabled, connectors])
  const done = (w) => !w.loading
  const ready = enabled && userLoaded && [persona, shortcuts, home, ui].every(done) && (pages.loaded || !!pages.off)
  return {
    ready, seen, update, mail,
    files: { persona: persona.doc, user, shortcuts: shortcuts.doc, home: home.doc, ui: ui.doc },
    pages: pages.pages,
  }
}

// "Make LocalAI yours": three example cards under the Home composer, a line of
// ready pages and "Hide these". A card puts its prompt into the composer and
// sends nothing. A card leaves once its own result exists; one that got its
// result during this visit says "Done" until the user leaves Home. The section
// never shows to a user who already changed LocalAI on their own, nor after
// "Hide these" (kept under seen in user.json).
export default function HomeExamples({ enabled, name, onPick, addToast }) {
  const { t } = useTranslation('workspace')
  const navigate = useNavigate()
  const st = useExampleState(enabled)
  const results = useMemo(() => exampleResults({ ...st.files, pages: st.pages }), [st.files, st.pages])
  // What existed when this visit's reads first answered: those cards are gone;
  // a result that arrives later shows "Done" for the rest of the visit.
  const atStart = useRef(null)
  const [visitExperienced, setVisitExperienced] = useState(null)
  useEffect(() => {
    if (!st.ready || atStart.current) return
    atStart.current = results
    setVisitExperienced(experienced(st.files))
  }, [st.ready, results, st.files])
  const [picked, setPicked] = useState('')
  const [copying, setCopying] = useState('')

  if (!enabled || !st.ready || visitExperienced === null || visitExperienced) return null
  if (examplesHidden(st.seen)) return null
  const start = atStart.current || results
  const cards = exampleCards({ t, name, mail: st.mail }).filter(c => !start[c.id])
  if (cards.length === 0) return null

  const pick = (card) => {
    setPicked(card.id)
    onPick(card.prompt)
  }
  const hide = async () => {
    try {
      await st.update(s => hideExamples(s, today()))
      showChange(addToast, {
        message: t('discovery.ex.hiddenToast'),
        testId: 'examples-hidden-toast',
        action: { label: t('discovery.action.undo'), onClick: () => { st.update(showExamples).catch(() => {}) } },
      })
    } catch (e) {
      addToast?.(t('discovery.ex.saveFailed', { message: e.message }), 'error')
    }
  }
  const ready = async (id) => {
    setCopying(id)
    try {
      const p = await pagesApi.copyStarter(id)
      await refreshPages()
      navigate(`/app/pages/${encodeURIComponent(p.name)}`)
    } catch (e) {
      addToast?.(t('discovery.ex.readyFailed', { message: e.message }), 'error')
    } finally {
      setCopying('')
    }
  }

  return (
    <section className="wk-ex" aria-labelledby="wk-ex-title" data-testid="home-examples">
      <header className="wk-ex__head">
        <h2 id="wk-ex-title" className="wk-ex__title">{t('discovery.ex.title')}</h2>
        <p className="wk-ex__lede">{t('discovery.ex.lede')}</p>
        <button type="button" className="wk-ex__hide" onClick={hide} data-testid="home-examples-hide">{t('discovery.ex.hide')}</button>
      </header>
      <ul className="wk-ex__cards">
        {cards.map(card => {
          const isDone = results[card.id]
          return (
            <li key={card.id} className="wk-ex__item">
              {isDone ? (
                <div className="wk-ex__card wk-ex__card--done" data-testid="home-example-done" data-card={card.id} role="status">
                  <span className="wk-ex__kicker"><span className="wk-ex__chip"><Icon name="check" /></span>{card.kicker}</span>
                  <p className="wk-ex__done">
                    {t('discovery.ex.donePrefix')}{' '}
                    <Link to="/app/account?tab=about">{t('discovery.ex.doneLink')}</Link>
                  </p>
                </div>
              ) : (
                <button
                  type="button" className={`wk-ex__card${picked === card.id ? ' wk-ex__card--picked' : ''}`}
                  onClick={() => pick(card)} data-testid="home-example" data-card={card.id}
                >
                  <span className="wk-ex__kicker"><span className="wk-ex__chip"><Icon name={card.icon} /></span>{card.kicker}</span>
                  <span className="wk-ex__quote">{t('discovery.ex.quote', { text: card.prompt })}</span>
                  <span className="wk-ex__what"><Icon name={card.whatIcon} />{card.what}</span>
                </button>
              )}
            </li>
          )
        })}
      </ul>
      <div className="wk-ex__ready">
        <span className="wk-ex__ready-label">{t('discovery.ex.ready')}</span>
        {READY.map(r => (
          <button
            key={r.id} type="button" className="home-ghost" onClick={() => ready(r.id)} disabled={!!copying}
            data-testid="home-example-ready" data-starter={r.id}
          >
            <Icon name={copying === r.id ? 'spinner' : r.icon} spin={copying === r.id} /> {t(`discovery.ex.${r.key}`)}
          </button>
        ))}
        <a className="home-ghost" href={DOCS_MAKE_YOURS} target="_blank" rel="noopener noreferrer" data-testid="home-examples-docs">
          <Icon name="help-circle" /> {t('discovery.ex.learn')}
        </a>
      </div>
    </section>
  )
}

// useExamplesGone says whether the examples are all done or hidden, for the
// "Customise Home" link of the library line. It shares the reads above.
export function useExamplesGone(enabled) {
  const st = useExampleState(enabled, { connectors: false })
  if (!enabled || !st.ready) return false
  const r = exampleResults({ ...st.files, pages: st.pages })
  return examplesHidden(st.seen) || (r.profile && r.page && r.schedule) || experienced(st.files)
}
