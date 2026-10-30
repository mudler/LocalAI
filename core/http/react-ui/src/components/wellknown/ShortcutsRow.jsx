/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Popover from '../Popover'
import ShortcutsEditor, { defaultStarters } from './ShortcutsEditor'
import { useWellKnown } from '../../hooks/useWellKnown'
import { shortcutList } from '../../utils/wellKnown'
import { SHORTCUT_ADDED_EVENT } from './changeNote'
import './wellKnown.css'

// ShortcutsRow is the one list of prompts above the composer: the ones of
// shortcuts.json, or the default starters when the user has none. A click
// fills the composer, or sends when the shortcut says so.
//
//   onPick(shortcut)        a chip was clicked
//   onEditWithAssistant()   the user wants the assistant to change the list
//   starters                the default prompts (the former starters)
//   filesOn                 files work on this server; without them only the
//                           defaults show and nothing can be edited
export default function ShortcutsRow({ onPick, onEditWithAssistant, starters, filesOn = true }) {
  const { t } = useTranslation('workspace')
  const { t: tc } = useTranslation('chat')
  const wk = useWellKnown('shortcuts', { enabled: filesOn })
  const [open, setOpen] = useState(false)
  const anchor = useRef(null)
  const defaults = starters || defaultStarters(tc)
  const { items, fromFile } = useMemo(() => shortcutList(wk.doc, defaults), [wk.doc, defaults])
  // A shortcut the assistant just added: the row is outlined for 4 seconds, a
  // static outline, so reduced motion needs nothing else.
  const [flash, setFlash] = useState(false)
  useEffect(() => {
    let timer = null
    const on = () => { setFlash(true); clearTimeout(timer); timer = setTimeout(() => setFlash(false), 4000) }
    window.addEventListener(SHORTCUT_ADDED_EVENT, on)
    return () => { window.removeEventListener(SHORTCUT_ADDED_EVENT, on); clearTimeout(timer) }
  }, [])

  return (
    <div className={`wk-shortcuts${flash ? ' wk-shortcuts--flash' : ''}`} data-testid="chat-shortcuts">
      {items.length > 0 && (
        <div className="wk-shortcuts__chips" role="list" aria-label={t('wellKnown.shortcuts.title')}>
          {items.map((s, i) => (
            <button
              key={`${i}-${s.label}`} type="button" role="listitem" className="wk-chip" title={s.prompt}
              data-testid="chat-shortcut" onClick={() => onPick(s)}
            >
              {s.icon && <Icon name={s.icon} />}
              <span>{s.label}</span>
            </button>
          ))}
        </div>
      )}
      {filesOn && (
        <div className="wk-shortcuts__note">
          {fromFile
            ? <span className="wk-src"><Icon name="file" /> {t('wellKnown.definedIn')} <code>shortcuts.json</code></span>
            : <span className="wk-src">{t('wellKnown.shortcuts.defaults')}</span>}
          {wk.doc && !wk.doc.valid && <span className="wk-src" role="status"><Icon name="warning" /> {t('wellKnown.shortcuts.invalid')}</span>}
          <button ref={anchor} type="button" className="wk-linkbtn" aria-expanded={open} onClick={() => setOpen(o => !o)} data-testid="chat-shortcuts-edit">
            <Icon name="sliders" /> {t('wellKnown.shortcuts.edit')}
          </button>
          {onEditWithAssistant && (
            <button type="button" className="wk-linkbtn" onClick={onEditWithAssistant} data-testid="chat-shortcuts-assistant">
              <Icon name="pencil" /> {t('wellKnown.editWithAssistant')}
            </button>
          )}
        </div>
      )}
      <Popover anchor={anchor} open={open} onClose={() => setOpen(false)} ariaLabel={t('wellKnown.shortcuts.edit')}>
        <ShortcutsEditor onDone={() => setOpen(false)} />
      </Popover>
    </div>
  )
}
