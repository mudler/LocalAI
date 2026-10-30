/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import WellKnownCard from './WellKnownCard'
import { applyAppearance } from './AppearanceSync'
import { useTheme } from '../../contexts/ThemeContext'
import { useModels } from '../../hooks/useModels'
import { CAP_CHAT } from '../../utils/capabilities'
import ShortcutsEditor from './ShortcutsEditor'
import HelpCard from './HelpCard'
import Icon from '../Icon'

function browserLanguage() {
  try { return navigator.language || '' } catch { return '' }
}

function browserZone() {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || '' } catch { return '' }
}

// The Account tab "About you and the assistant": persona.json and user.json.
// The language and the time zone come from the browser until the user saves.
export function AboutYouTab() {
  const { t } = useTranslation('workspace')
  const fromBrowser = t('wellKnown.from.browser')
  const prefillUser = useCallback((doc, file) => {
    const values = {}
    const notes = {}
    if (!file.language && browserLanguage()) { values.language = browserLanguage(); notes.language = fromBrowser }
    if (!file.timezone && browserZone()) { values.timezone = browserZone(); notes.timezone = fromBrowser }
    return { values, notes }
  }, [fromBrowser])

  return (
    <div className="wk-tab" data-testid="account-about">
      <div className="wk-grid">
        <WellKnownCard name="persona" title={t('wellKnown.persona.title')} lede={t('wellKnown.persona.lede')} />
        <WellKnownCard name="user" title={t('wellKnown.user.title')} lede={t('wellKnown.user.lede')} prefill={prefillUser}>
          <p className="wk-note wk-note--info wk-privacy" data-testid="wk-privacy">
            <Icon name="info" /> <span>{t('discovery.privacy')}</span>
          </p>
        </WellKnownCard>
      </div>
      <div className="wk-grid wk-gap">
        <HelpCard />
        <section className="wk-card" aria-labelledby="wk-shortcuts-title" data-testid="wk-card-shortcuts">
          <header className="wk-card__head">
            <div className="wk-card__titles">
              <h2 id="wk-shortcuts-title">{t('wellKnown.shortcuts.title')}</h2>
              <p>{t('wellKnown.shortcuts.lede')}</p>
            </div>
          </header>
          <ShortcutsEditor inline />
        </section>
      </div>
    </div>
  )
}

// The Account tab "Appearance": ui.json. A change shows at once; leaving the
// tab without saving puts the saved look back.
export function AppearanceTab() {
  const { t } = useTranslation('workspace')
  const { setTheme } = useTheme()
  const { models } = useModels(CAP_CHAT)
  const saved = useRef({})
  const options = useMemo(() => ({ defaultModel: models.map(m => ({ value: m.id, label: m.id })) }), [models])

  useEffect(() => () => applyAppearance(saved.current, setTheme), [setTheme])
  const onChange = useCallback((v) => applyAppearance(v, setTheme), [setTheme])
  const onSaved = useCallback((doc) => { saved.current = doc.data }, [])
  const prefill = useCallback((doc) => { saved.current = doc.data; return { values: {}, notes: {} } }, [])

  return (
    <div className="wk-grid wk-grid--one" data-testid="account-appearance">
      <WellKnownCard name="ui" title={t('wellKnown.ui.title')} lede={t('wellKnown.ui.lede')} options={options} onChange={onChange} onSaved={onSaved} prefill={prefill} />
    </div>
  )
}
