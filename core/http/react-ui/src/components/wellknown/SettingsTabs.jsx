/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import WellKnownCard from './WellKnownCard'
import { applyAppearance } from './AppearanceSync'
import { useTheme } from '../../contexts/ThemeContext'
import ShortcutsEditor from './ShortcutsEditor'
import HelpCard from './HelpCard'
import NavigationEditor from './NavigationEditor'
import CommandsEditor from './CommandsEditor'
import SlotsEditor from './SlotsEditor'
import { IdentityPreview, LookPreview, StringsPreview } from './Previews'
import Icon from '../Icon'

const HEADLINE_KEYS = ['headline']
const TEXT_KEYS = ['strings']
// The keys of ui.json that have a tab of their own: the generic Appearance form leaves them out.
const OWN_TAB_KEYS = ['strings', 'sound', 'voice']

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

  // The forms' values as they are edited, for the preview.
  const [persona, setPersona] = useState(null)
  const [home, setHome] = useState(null)

  return (
    <div className="wk-tab" data-testid="account-about">
      <div className="wk-grid wk-grid--preview">
        <WellKnownCard name="persona" title={t('wellKnown.persona.title')} lede={t('wellKnown.persona.lede')} onChange={setPersona} footer />
        <IdentityPreview persona={persona} home={home} />
      </div>
      <div className="wk-grid wk-gap">
        <WellKnownCard name="home" testId="wk-card-home-headline" title={t('wellKnown.headline.title')} lede={t('wellKnown.headline.lede')} only={HEADLINE_KEYS} onChange={setHome} footer />
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

// The Account tab "Appearance": ui.json, without its text overrides (the Text
// tab). A change shows at once and in the preview; leaving the tab without
// saving puts the saved look back.
export function AppearanceTab() {
  const { t } = useTranslation('workspace')
  const { setTheme } = useTheme()
  const saved = useRef({})

  useEffect(() => () => applyAppearance(saved.current, setTheme), [setTheme])
  const onChange = useCallback((v) => applyAppearance(v, setTheme), [setTheme])
  const onSaved = useCallback((doc) => { saved.current = doc.data }, [])
  const prefill = useCallback((doc) => { saved.current = doc.data; return { values: {}, notes: {} } }, [])

  return (
    <div className="wk-grid wk-grid--preview" data-testid="account-appearance">
      <WellKnownCard name="ui" title={t('wellKnown.ui.title')} lede={t('wellKnown.ui.lede')} onChange={onChange} onSaved={onSaved} prefill={prefill} omit={OWN_TAB_KEYS} footer />
      <LookPreview />
    </div>
  )
}

// The Account tab "Navigation": nav.json, the order and names of the sidebar.
export function NavigationTab() {
  return (
    <div className="wk-grid wk-grid--wide" data-testid="account-navigation">
      <NavigationEditor />
    </div>
  )
}

// The Account tab "Slots": the slots of ui.json, page tiles, tool buttons and
// commands in the chat header, the sidebar footer and the composer toolbar.
export function SlotsTab() {
  return (
    <div className="wk-grid wk-grid--wide" data-testid="account-slots">
      <SlotsEditor />
    </div>
  )
}

// The Account tab "Text and commands": the text overrides of ui.json (strings)
// and the slash commands of commands.json. Only the keys of the server's list
// show, and a save keeps every other key of the file.
export function StringsTab() {
  const { t } = useTranslation('workspace')
  const [ui, setUi] = useState(null)
  return (
    <div data-testid="account-strings">
      <div className="wk-grid wk-grid--preview">
        <WellKnownCard name="ui" testId="wk-card-strings" title={t('wellKnown.strings.title')} lede={t('wellKnown.strings.lede')} only={TEXT_KEYS} onChange={setUi} footer />
        <StringsPreview ui={ui} />
      </div>
      <CommandsEditor />
    </div>
  )
}
