import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { useTranslation, Trans } from 'react-i18next'
import { availableActions, filterActions, SLASH_GROUPS } from './homeActions'
import Icon from '../Icon'

// The Enter glyph is not in the kit sprite. A return arrow in a darker cell
// teaches the shortcut next to the label.
// eslint-disable-next-line no-unused-vars
function EnterGlyph() {
  return (
    <svg className="home-send-btn__glyph" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M19 5v7a3 3 0 01-3 3H5M9 11l-4 4 4 4" />
    </svg>
  )
}

// eslint-disable-next-line no-unused-vars
function Marked({ text, upto }) {
  if (!upto) return text
  return (<><mark>{text.slice(0, upto)}</mark>{text.slice(upto)}</>)
}

// The command bar: model chip, MCP chip, message box, attach buttons, Send and
// the slash menu. It owns only the transient menu state; the message, files and
// what Send does belong to the page.
export default function HomeComposer({
  message, onMessage, onSubmit, canSend, sending, sendTitle, textareaRef,
  picker, mcp, files, onRemoveFile, onAttach,
  placeholder, slashContext, onRunAction,
}) {
  const { t } = useTranslation('home')
  const imageRef = useRef(null)
  const audioRef = useRef(null)
  const fileRef = useRef(null)
  const [active, setActive] = useState(0)
  const [dismissed, setDismissed] = useState(false)

  const labelOf = useCallback((a) => t(`slash.${a.id}.label`), [t])
  const actions = useMemo(() => availableActions(slashContext), [slashContext])
  // The menu belongs to a message that is only a command: it starts with a
  // slash and holds no space. "/etc/hosts how do I..." is just a message.
  const query = message.startsWith('/') && !/\s/.test(message) ? message.slice(1).toLowerCase() : null
  const items = useMemo(
    () => (query === null ? [] : filterActions(actions, query, labelOf)),
    [query, actions, labelOf],
  )
  const open = query !== null && items.length > 0 && !dismissed
  const safeActive = Math.min(active, Math.max(0, items.length - 1))

  useEffect(() => { setActive(0); setDismissed(false) }, [message])

  useEffect(() => {
    if (!open) return
    document.getElementById(`home-slash-opt-${safeActive}`)?.scrollIntoView?.({ block: 'nearest' })
  }, [open, safeActive])

  // Copy the list before clearing the input, which empties the live FileList.
  const attach = (kind, input) => {
    const list = Array.from(input.files || [])
    input.value = ''
    if (list.length) onAttach(kind, list)
  }

  const run = (action) => {
    onMessage('')
    onRunAction(action.id)
  }

  const onKeyDown = (e) => {
    if (e.nativeEvent.isComposing) return
    if (open) {
      const n = items.length
      if (e.key === 'ArrowDown') { e.preventDefault(); setActive((safeActive + 1) % n); return }
      if (e.key === 'ArrowUp') { e.preventDefault(); setActive((safeActive + n - 1) % n); return }
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); run(items[safeActive]); return }
      if (e.key === 'Escape') { e.preventDefault(); setDismissed(true); return }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      onSubmit()
    }
  }

  const activeId = open ? `home-slash-opt-${safeActive}` : undefined

  // Group labels appear once, above the first item of each group.
  const groupStart = {}
  items.forEach((a, i) => { if (!(a.group in groupStart)) groupStart[a.group] = i })

  return (
    <form
      className="home-cmd"
      onSubmit={(e) => { e.preventDefault(); onSubmit() }}
      autoComplete="off"
      data-testid="home-composer"
    >
      <div className="home-cmd__top">
        {picker}
        {mcp}
        <span className="home-slash-hint">
          <Trans i18nKey="input.slashHint" t={t} components={{ kbd: <kbd className="dk-kbd" /> }} />
        </span>
      </div>

      {files.length > 0 && (
        <div className="home-file-tags">
          {files.map((f, i) => (
            <span key={i} className="home-file-tag">
              <Icon name={f.type?.startsWith('image/') ? 'image' : f.type?.startsWith('audio/') ? 'mic' : 'file'} />
              {f.name}
              <button type="button" onClick={() => onRemoveFile(f)} aria-label={t('input.removeFile', { name: f.name })}>
                <Icon name="close" />
              </button>
            </span>
          ))}
        </div>
      )}

      <div className="home-well">
        <textarea
          ref={textareaRef}
          className="home-textarea"
          value={message}
          onChange={(e) => onMessage(e.target.value)}
          placeholder={placeholder || t('input.placeholder')}
          aria-label={t('input.label')}
          rows={3}
          onKeyDown={onKeyDown}
          role="combobox"
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-controls="home-slash"
          aria-autocomplete="list"
          aria-activedescendant={activeId}
        />
        {open && (
          <div className="dk-cmdlist home-slash" id="home-slash" role="listbox" aria-label={t('slash.label')}>
            <div className="home-slash__scroll">
            {SLASH_GROUPS.filter(g => g in groupStart).map(g => (
              <div className="dk-cmd-group" role="group" aria-labelledby={`home-slash-group-${g}`} key={g}>
                <div className="dk-cmd-label" id={`home-slash-group-${g}`}>{t(`slash.group.${g}`)}</div>
                {items.map((a, i) => a.group !== g ? null : (
                  <div
                    key={a.id}
                    id={`home-slash-opt-${i}`}
                    className="dk-cmd"
                    role="option"
                    aria-selected={i === safeActive}
                    data-testid={`home-slash-${a.id}`}
                    // mousedown keeps focus in the textarea, so the click lands.
                    onMouseDown={(e) => e.preventDefault()}
                    onMouseMove={() => setActive(i)}
                    onClick={() => run(a)}
                  >
                    <Icon name={a.icon} className="dk-icon" />
                    <span className="dk-cmd-main">
                      <span className="dk-cmd-name dk-mono"><Marked text={a.cmd} upto={query && a.cmd.slice(1).startsWith(query) ? 1 + query.length : 0} /></span>
                      <span className="dk-cmd-desc">{t(`slash.${a.id}.desc`)}</span>
                    </span>
                    <span className="dk-cmd-keys"><kbd className="dk-kbd">↵</kbd></span>
                  </div>
                ))}
              </div>
            ))}
            </div>
            <div className="dk-cmd-foot">
              <span><kbd className="dk-kbd">↑</kbd><kbd className="dk-kbd">↓</kbd> {t('slash.move')}</span>
              <span><kbd className="dk-kbd">esc</kbd> {t('slash.close')}</span>
            </div>
          </div>
        )}
      </div>

      <div className="home-cmd__foot">
        <div className="home-attach-buttons">
          <button type="button" className="home-attach-btn" onClick={() => imageRef.current?.click()} title={t('input.attachImage')} aria-label={t('input.attachImage')}>
            <Icon name="image" />
          </button>
          <button type="button" className="home-attach-btn" onClick={() => audioRef.current?.click()} title={t('input.attachAudio')} aria-label={t('input.attachAudio')}>
            <Icon name="mic" />
          </button>
          <button type="button" className="home-attach-btn" onClick={() => fileRef.current?.click()} title={t('input.attachFile')} aria-label={t('input.attachFile')}>
            <Icon name="file" />
          </button>
        </div>
        <span className="home-input-hint">{t('input.enterHint')}</span>
        <button
          type="submit"
          className="home-send-btn"
          data-empty={message.trim() || files.length > 0 ? undefined : 'true'}
          data-loading={sending ? 'true' : undefined}
          disabled={!canSend}
          title={sendTitle}
        >
          <span className="home-send-btn__label">{t('input.send')}</span>
          <span className="home-send-btn__cell"><EnterGlyph /></span>
        </button>
      </div>
      <input ref={imageRef} type="file" multiple accept="image/*" className="hidden" onChange={(e) => { attach('image', e.target) }} />
      <input ref={audioRef} type="file" multiple accept="audio/*" className="hidden" onChange={(e) => { attach('audio', e.target) }} />
      <input ref={fileRef} type="file" multiple accept=".txt,.md,.pdf" className="hidden" onChange={(e) => { attach('file', e.target) }} />
    </form>
  )
}
