/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Icon from '../Icon'
import { Button, Switch } from '../ui'
import { useViewAs } from '../../contexts/ViewAsContext'
import { viewAsStore } from '../../utils/viewAsStore'
import { IDLE_MS } from '../../utils/viewAs'
import './viewas.css'

// The bar of "View as". It sits above the whole app shell, on every page and
// on Administration too, and cannot be dismissed: the only ways out are Exit
// and the end of the mode. Read-only wears the app accent and acting wears the
// error colour; the tag, the words and the icon carry the mode as well, so
// colour is never the only signal.
//
// Turning "Allow changes" on asks first (the dialog's first focus is on "Stay
// read-only"); turning it off is one press.
export default function ViewAsBar({ addToast }) {
  const { t } = useTranslation('viewas')
  const view = useViewAs()
  const { active, acting, target } = view
  const [asking, setAsking] = useState(false)
  const barRef = useRef(null)
  const switchLabel = useId()

  // The sidebar and the page height start under the bar, so the bar's height
  // is a variable of the document. It changes when the message wraps.
  useLayoutEffect(() => {
    const root = document.documentElement
    const bar = barRef.current
    if (!active || !bar) { root.classList.remove('has-viewas'); root.style.removeProperty('--viewas-h'); return undefined }
    const measure = () => root.style.setProperty('--viewas-h', `${bar.offsetHeight}px`)
    root.classList.add('has-viewas')
    measure()
    const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(measure) : null
    observer?.observe(bar)
    return () => {
      observer?.disconnect()
      root.classList.remove('has-viewas')
      root.style.removeProperty('--viewas-h')
    }
  }, [active])

  // What happens to the mode without a press: a write stopped in read-only,
  // acting that timed out, a viewed user that is gone.
  useEffect(() => viewAsStore.onBlocked(({ name }) => addToast?.(t('write.blocked', { name }), 'warning')), [addToast, t])
  useEffect(() => {
    if (view.reverted === 'idle') addToast?.(t('notice.idle', { minutes: Math.round(IDLE_MS / 60000) }), 'warning')
    if (view.ended) addToast?.(t('notice.ended', { name: view.ended.name }), 'error')
    if (view.reverted || view.ended) view.clear()
  }, [view, addToast, t])
  useEffect(() => { if (!acting) setAsking(false) }, [acting])

  if (!active) return null
  const name = target.name
  const onSwitch = (next) => {
    if (next) setAsking(true)
    else view.revoke()
  }

  return (
    <>
      <div ref={barRef} className="viewas-bar" data-mode={acting ? 'actas' : 'readonly'} data-testid="viewas-bar" role="region" aria-label={t('bar.region')}>
        <div className="viewas-bar__msg" role="status" data-testid="viewas-status">
          <Icon name={acting ? 'warning' : 'eye'} className="viewas-bar__icon" aria-hidden="true" />
          <span className="viewas-bar__tag" data-testid="viewas-tag">{acting ? t('bar.actingTag') : t('bar.readonlyTag')}</span>
          <span className="viewas-bar__text">
            <strong>{acting ? t('bar.actingLead', { name }) : t('bar.readonlyLead', { name })}</strong>
            {' '}{acting ? t('bar.actingRest', { name }) : t('bar.readonlyRest')}
          </span>
        </div>
        <div className="viewas-bar__acts">
          <span className="viewas-bar__switch">
            <Switch checked={acting} onChange={onSwitch} labelledBy={switchLabel} testId="viewas-allow" />
            <span id={switchLabel}>{t('bar.allow')}</span>
          </span>
          <Button size="sm" icon="log-out" onClick={view.exit} aria-label={t('bar.exitLabel', { name })} data-testid="viewas-exit">{t('bar.exit')}</Button>
        </div>
      </div>
      {asking && (
        <Dialog
          role="alertdialog" labelId="viewas-confirm-title" testId="viewas-confirm" closeLabel={t('confirm.close')}
          title={t('confirm.title', { name })} description={t('confirm.intro', { name })}
          onClose={() => setAsking(false)}
          foot={(
            <>
              <Button data-autofocus onClick={() => setAsking(false)} data-testid="viewas-stay">{t('confirm.stay')}</Button>
              <Button variant="danger" icon="warning" onClick={() => { view.allow(); setAsking(false) }} data-testid="viewas-confirm-allow">{t('confirm.allow')}</Button>
            </>
          )}
        >
          <ul className="viewas-list">
            <li><Icon name="edit" aria-hidden="true" />{t('confirm.can', { name })}</li>
            <li><Icon name="history" aria-hidden="true" />{t('confirm.logged', { name })}</li>
            <li><Icon name="eye" aria-hidden="true" />{t('confirm.back')}</li>
          </ul>
        </Dialog>
      )}
    </>
  )
}
