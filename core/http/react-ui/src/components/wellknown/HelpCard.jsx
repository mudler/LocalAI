/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { apiUrl } from '../../utils/basePath'
import { useSeen } from '../../hooks/useSeen'
import {
  DOCS_MAKE_YOURS, dismissedCount, examplesHidden, forgetHints, showExamples, suggestionsOn, withSeen,
} from '../../utils/discovery'
import './wellKnown.css'

// The server's default for offers, for users who did not choose.
function useServerDefault() {
  const [on, setOn] = useState(true)
  useEffect(() => {
    let live = true
    fetch(apiUrl('/api/features')).then(r => r.json()).then((f) => { if (live) setOn(f?.chats_suggestions !== false) }).catch(() => {})
    return () => { live = false }
  }, [])
  return on
}

function Row({ id, title, hint, children }) {
  return (
    <div className="wk-help__row">
      <div className="wk-help__text">
        <span className="wk-help__title" id={`${id}-label`}>{title}</span>
        <span className="wk-help__hint" id={`${id}-hint`}>{hint}</span>
      </div>
      {children}
    </div>
  )
}

// HelpCard is "Help and suggestions" of Account, About you and the assistant:
// the ways LocalAI shows what it can do, each with its off switch. Everything
// is kept under seen in user.json, the record the server and the assistant
// read too.
export default function HelpCard() {
  const { t } = useTranslation('workspace')
  const { seen, loaded, update } = useSeen()
  const serverOn = useServerDefault()
  const [problem, setProblem] = useState('')
  const [done, setDone] = useState('')

  const change = async (fn, note = '') => {
    setProblem('')
    setDone('')
    try {
      await update(fn)
      if (note) setDone(note)
    } catch (e) {
      setProblem(e.message)
    }
  }
  const offers = suggestionsOn(seen, serverOn)
  const notes = !seen.new_off
  const hidden = examplesHidden(seen)
  const dismissed = dismissedCount(seen)
  // "Turn them back on in Account" lands here.
  useEffect(() => {
    if (loaded && window.location.hash === '#help') document.getElementById('help')?.scrollIntoView?.({ block: 'start' })
  }, [loaded])
  const learned = dismissed > 0 || !!seen.last_offer || (Array.isArray(seen.offer_chats) && seen.offer_chats.length > 0)

  return (
    <section className="wk-card wk-help" aria-labelledby="wk-help-title" id="help" data-testid="wk-card-help">
      <header className="wk-card__head">
        <div className="wk-card__titles">
          <h2 id="wk-help-title">{t('discovery.help.title')}</h2>
          <p>{t('discovery.help.sub')}</p>
        </div>
      </header>
      <div className="wk-meta"><span className="wk-src"><Icon name="file" /> {t('wellKnown.storedIn')} <code>user.json</code></span></div>
      {!loaded ? null : (
        <div className="wk-help__rows">
          <Row id="wk-help-offers" title={t('discovery.help.offers')} hint={t('discovery.help.offersHint')}>
            <button
              type="button" className="dk-switch" role="switch" aria-checked={offers}
              aria-labelledby="wk-help-offers-label" aria-describedby="wk-help-offers-hint"
              onClick={() => change(s => withSeen(s, { offers_off: offers }))} data-testid="wk-help-offers"
            />
          </Row>
          <Row id="wk-help-new" title={t('discovery.help.new')} hint={t('discovery.help.newHint')}>
            <button
              type="button" className="dk-switch" role="switch" aria-checked={notes}
              aria-labelledby="wk-help-new-label" aria-describedby="wk-help-new-hint"
              onClick={() => change(s => withSeen(s, { new_off: notes }))} data-testid="wk-help-new"
            />
          </Row>
          <Row id="wk-help-examples" title={t('discovery.help.examples')} hint={hidden ? t('discovery.help.examplesHidden') : t('discovery.help.examplesShown')}>
            <button
              type="button" className="dk-btn dk-btn--sm dk-btn--secondary" disabled={!hidden}
              onClick={() => change(showExamples, t('discovery.help.examplesBack'))} data-testid="wk-help-examples"
            >
              {t('discovery.help.showExamples')}
            </button>
          </Row>
          <Row id="wk-help-learned" title={t('discovery.help.dismissed')} hint={dismissed > 0 ? t('discovery.help.dismissedHint', { count: dismissed }) : t('discovery.help.dismissedNone')}>
            <button
              type="button" className="dk-btn dk-btn--sm dk-btn--secondary" disabled={!learned}
              onClick={() => change(forgetHints, t('discovery.help.forgotten'))} data-testid="wk-help-forget"
            >
              {t('discovery.help.forget')}
            </button>
          </Row>
        </div>
      )}
      {problem && <p className="wk-note wk-note--error" role="alert"><Icon name="alert-circle" /> {problem}</p>}
      <p className="wk-help__status" role="status">{done}</p>
      <p className="wk-help__learn">
        <Icon name="book" /> {t('discovery.help.learn')}{' '}
        <a href={DOCS_MAKE_YOURS} target="_blank" rel="noopener noreferrer">{t('discovery.ex.title')}</a>
      </p>
    </section>
  )
}
