import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { useTranslation, Trans } from 'react-i18next'
import { availableActions, SLASH_GROUPS } from './homeActions'
import { filterCommands, slashQuery } from '../../utils/slashCommands'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import Button from '../ui/Button'
import './composer.css'
// eslint-disable-next-line no-unused-vars
import DictateButton from './DictateButton'
// eslint-disable-next-line no-unused-vars
import { StagedAttachment } from '../chat/Attachments'

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
//
// Home uses it as is. Chat uses the same bar and adds what a conversation
// needs, all optional:
//   chips        more chips after the MCP chip (the agent picker and the files chip)
//   slash        { actions, groups, label, desc, groupLabel, ctx, argsLabel,
//                argLabel, badgeLabel, noteLabel } for a menu of registry
//                commands (utils/slashCommands.js); ctx goes to the argument
//                providers
//   onClearFiles empties the staged files ("/attach clear")
//   streaming    swaps Send for a Stop button that calls onStop; with
//                queueWhileStreaming, Send stays once there is text to queue
//   onPaste, onKeyDownExtra (return true to take the key), fileAccept, rows,
//   anyFile      one attach button for files of any type, in place of the
//                image, audio and file buttons; its dialog has no type
//                filter, because some file dialogs read accept="*/*" as a
//                filter and grey out archives such as .zip
//   onDictateError (message for a failed dictation, e.g. a toast),
//   onDictate (takes the dictated text instead of the box; the callback of
//                the render where the recording started gets it),
//   testIds and class names for the pieces tests and the thread reach for
//   extras       a node drawn after the attach buttons (the add-on slot)
export default function HomeComposer({
  message, onMessage, onSubmit, canSend, sending, sendTitle, textareaRef,
  picker, mcp, chips, files = [], onRemoveFile, onAttach,
  placeholder, slashContext, onRunAction, slash,
  streaming = false, onStop, stopTitle, onPaste, onKeyDownExtra,
  fileAccept = '.txt,.md,.pdf', rows = 3, strictEnter = false, testId = 'home-composer',
  textareaTestId, textareaClass = '', sendId, sendTestId, hint, onDictateError, extras,
  attachDisabled = false, attachTitle, fileTestId, queueWhileStreaming = false, onDictate, anyFile = false, onClearFiles,
}) {
  const { t } = useTranslation('home')
  const imageRef = useRef(null)
  const audioRef = useRef(null)
  const fileRef = useRef(null)
  const [active, setActive] = useState(null)
  const [dismissed, setDismissed] = useState(false)

  // The menu: the commands of the registry (utils/slashCommands.js) that the
  // page passes in slash, or Home's own list when it passes none. "/cmd " of
  // a command with an argument provider lists its arguments instead.
  const groupList = slash?.groups || SLASH_GROUPS
  const labelOf = useCallback((a) => (a.label || (slash ? slash.label(a) : t(`slash.${a.id}.label`))), [t, slash])
  const descOf = (a) => (a.label ? a.desc : (slash ? slash.desc(a) : t(`slash.${a.id}.desc`)))
  const groupLabel = (g) => (slash ? slash.groupLabel(g) : t(`slash.group.${g}`))
  const actions = useMemo(
    () => (slash ? slash.actions : availableActions(slashContext)),
    [slash, slashContext],
  )
  const sq = useMemo(() => slashQuery(message, actions), [message, actions])
  const argCmd = sq?.mode === 'args' ? sq.command : null
  const argQuery = sq?.mode === 'args' ? sq.query : ''
  const [args, setArgs] = useState({ id: '', query: '', items: [] })
  useEffect(() => {
    if (!argCmd) return undefined
    let live = true
    Promise.resolve(argCmd.args(argQuery, slash?.ctx || {}))
      .then((items) => { if (live) setArgs({ id: argCmd.id, query: argQuery, items: items || [] }) })
      .catch(() => { if (live) setArgs({ id: argCmd.id, query: argQuery, items: [] }) })
    return () => { live = false }
  }, [argCmd, argQuery, slash?.ctx])
  const items = useMemo(() => {
    if (!sq) return []
    if (sq.mode === 'commands') return filterCommands(actions, sq.query, labelOf, groupList)
    return args.id === argCmd.id && args.query === argQuery ? args.items : []
  }, [sq, actions, labelOf, groupList, args, argCmd, argQuery])
  const query = sq?.mode === 'commands' ? sq.query : null
  const open = sq !== null && items.length > 0 && !dismissed
  // The row that starts selected: the first command whose name starts with
  // what was typed, before those that only match by their label.
  const preferred = query ? Math.max(0, items.findIndex((a) => a.cmd?.slice(1).toLowerCase().startsWith(query))) : 0
  const safeActive = Math.min(active === null ? preferred : active, Math.max(0, items.length - 1))

  useEffect(() => { setActive(null); setDismissed(false) }, [message])

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

  // A command the composer runs itself: the attach dialog and its "clear".
  const openFiles = () => (anyFile ? fileRef : imageRef).current?.click()
  const runLocal = (id, item) => {
    if (id === 'attach' && !item) { openFiles(); return true }
    if (item?.action === 'clearFiles') { onClearFiles?.(); return true }
    return false
  }

  // Enter runs a command (or puts its text in the box); Tab completes it:
  // the text of an insert command, "/cmd " for one that takes arguments.
  const run = (entry, complete = false) => {
    if (argCmd) {
      if (entry.insert) { onMessage(entry.insert); return }
      onMessage('')
      if (!runLocal(argCmd.id, entry)) onRunAction(argCmd.id, entry)
      return
    }
    if (entry.insert) { onMessage(entry.insert); return }
    if (complete) { onMessage(entry.args ? `${entry.cmd} ` : entry.cmd); return }
    onMessage('')
    if (!runLocal(entry.id)) onRunAction(entry.id)
  }

  const onKeyDown = (e) => {
    if (e.nativeEvent.isComposing) return
    if (!open && onKeyDownExtra?.(e)) return
    if (open) {
      const n = items.length
      if (e.key === 'ArrowDown') { e.preventDefault(); setActive((safeActive + 1) % n); return }
      if (e.key === 'ArrowUp') { e.preventDefault(); setActive((safeActive + n - 1) % n); return }
      if (e.key === 'Enter') { e.preventDefault(); run(items[safeActive]); return }
      if (e.key === 'Tab') { e.preventDefault(); run(items[safeActive], true); return }
      if (e.key === 'Escape') { e.preventDefault(); setDismissed(true); return }
    }
    // With strictEnter only a bare Enter sends: Ctrl, Cmd and Alt Enter add a
    // line, as the Chat box always did.
    const bare = !strictEnter || (!e.ctrlKey && !e.metaKey && !e.altKey && e.keyCode !== 229)
    if (e.key === 'Enter' && !e.shiftKey && bare) {
      e.preventDefault()
      onSubmit()
    }
  }

  const activeId = open ? `home-slash-opt-${safeActive}` : undefined

  // The rows of the menu: commands under their group labels, or the arguments
  // of one command under its name.
  const rowsOf = argCmd
    ? [{ group: 'args', label: slash?.argsLabel?.(argCmd) || argCmd.cmd, rows: items.map((it, i) => ({ it, i })) }]
    : groupList.map((g) => ({ group: g, label: groupLabel(g), rows: items.map((it, i) => ({ it, i })).filter(({ it }) => it.group === g) }))
        .filter((grp) => grp.rows.length > 0)
  const rowView = (it) => (argCmd
    ? { testId: `home-slash-arg-${it.key}`, icon: it.icon || argCmd.icon, name: it.name || slash?.argLabel?.(argCmd, it) || '', desc: it.desc || slash?.argDesc?.(argCmd, it) || '', mono: !!it.name, badge: it.badge, note: it.note, upto: 0 }
    : { testId: `home-slash-${it.id}`, icon: it.icon, name: it.cmd, desc: descOf(it), mono: true, badge: it.badge, note: it.note,
      upto: query && it.cmd.slice(1).toLowerCase().startsWith(query) ? 1 + query.length : 0 })

  return (
    <form
      className="home-cmd"
      onSubmit={(e) => { e.preventDefault(); onSubmit() }}
      autoComplete="off"
      data-testid={testId}
      // The toast stack stays above this bar on a chat page (components/Toast).
      // A page can mark a larger box around it instead; the outer mark wins.
      data-toast-anchor=""
    >
      <div className="home-cmd__top">
        {picker}
        {mcp}
        {chips}
        <span className="home-slash-hint">
          <Trans i18nKey="input.slashHint" t={t} components={{ kbd: <kbd className="dk-kbd" /> }} />
        </span>
      </div>

      {files.length > 0 && (
        <div className="home-file-tags">
          {files.map((f, i) => (
            <StagedAttachment key={i} file={f} onRemove={() => onRemoveFile(f)} removeLabel={t('input.removeFile', { name: f.name })} />
          ))}
        </div>
      )}

      <div className="home-well">
        <textarea
          ref={textareaRef}
          className={`home-textarea${textareaClass ? ` ${textareaClass}` : ''}`}
          data-testid={textareaTestId}
          value={message}
          onChange={(e) => onMessage(e.target.value)}
          placeholder={placeholder || t('input.placeholder')}
          aria-label={t('input.label')}
          rows={rows}
          onKeyDown={onKeyDown}
          onPaste={onPaste}
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
            {rowsOf.map((grp) => (
              <div className="dk-cmd-group" role="group" aria-labelledby={`home-slash-group-${grp.group}`} key={grp.group}>
                <div className="dk-cmd-label" id={`home-slash-group-${grp.group}`}>{grp.label}</div>
                {grp.rows.map(({ it, i }) => {
                  const v = rowView(it)
                  return (
                    <div
                      key={v.testId}
                      id={`home-slash-opt-${i}`}
                      className="dk-cmd"
                      role="option"
                      aria-selected={i === safeActive}
                      data-testid={v.testId}
                      // mousedown keeps focus in the textarea, so the click lands.
                      onMouseDown={(e) => e.preventDefault()}
                      onMouseMove={() => setActive(i)}
                      onClick={() => run(it)}
                    >
                      <Icon name={v.icon} className="dk-icon" />
                      <span className="dk-cmd-main">
                        <span className={`dk-cmd-name${v.mono ? ' dk-mono' : ''}`}><Marked text={v.name} upto={v.upto} /></span>
                        {v.desc && <span className="dk-cmd-desc">{v.desc}</span>}
                      </span>
                      {(v.badge || v.note) && (
                        <span className="home-slash__tags">
                          {v.note && <span className="home-slash__note">{slash?.noteLabel?.(v.note) || v.note}</span>}
                          {v.badge && <span className="dk-badge home-slash__badge">{slash?.badgeLabel?.(v.badge) || v.badge}</span>}
                        </span>
                      )}
                      <span className="dk-cmd-keys"><kbd className="dk-kbd">↵</kbd></span>
                    </div>
                  )
                })}
              </div>
            ))}
            </div>
            <div className="dk-cmd-foot">
              <span><kbd className="dk-kbd">↑</kbd><kbd className="dk-kbd">↓</kbd> {t('slash.move')}</span>
              <span><kbd className="dk-kbd">tab</kbd> {t('slash.complete')}</span>
              <span><kbd className="dk-kbd">esc</kbd> {t('slash.close')}</span>
            </div>
          </div>
        )}
      </div>

      <div className="home-cmd__foot">
        <div className="home-attach-buttons">
          {onAttach && anyFile && (
            <Button variant="ghost" size="sm" icon="file" className="home-attach-btn" disabled={attachDisabled} onClick={() => fileRef.current?.click()} title={attachTitle || t('input.attachFile')} aria-label={t('input.attachFile')} />
          )}
          {onAttach && !anyFile && (
            <>
              <Button variant="ghost" size="sm" icon="image" className="home-attach-btn" disabled={attachDisabled} onClick={() => imageRef.current?.click()} title={attachTitle || t('input.attachImage')} aria-label={t('input.attachImage')} />
              <Button variant="ghost" size="sm" icon="music" className="home-attach-btn" disabled={attachDisabled} onClick={() => audioRef.current?.click()} title={attachTitle || t('input.attachAudio')} aria-label={t('input.attachAudio')} />
              <Button variant="ghost" size="sm" icon="file" className="home-attach-btn" disabled={attachDisabled} onClick={() => fileRef.current?.click()} title={attachTitle || t('input.attachFile')} aria-label={t('input.attachFile')} />
            </>
          )}
          <DictateButton
            onText={onDictate || ((text) => onMessage(message.trim() ? `${message.trimEnd()} ${text}` : text))}
            onError={onDictateError}
          />
          {extras}
        </div>
        <span className="home-input-hint">{hint || t('input.enterHint')}</span>
        {streaming && (
          <button
            type="button"
            className="home-stop-btn"
            id={queueWhileStreaming ? undefined : sendId}
            data-testid="chat-stop"
            onClick={onStop}
            title={stopTitle}
          >
            <Icon name="stop" />
            <span>{t('input.stop')}</span>
            <kbd className="dk-kbd">esc</kbd>
          </button>
        )}
        {/* A chat the server keeps takes messages while it works: they wait
            and reach the model at its next step (queueWhileStreaming). */}
        {(!streaming || (queueWhileStreaming && (message.trim() || files.length > 0))) && (
          <button
            type="submit"
            className="home-send-btn"
            id={sendId}
            data-testid={sendTestId}
            data-empty={message.trim() || files.length > 0 ? undefined : 'true'}
            data-loading={sending ? 'true' : undefined}
            disabled={!canSend}
            title={sendTitle}
          >
            <span className="home-send-btn__label">{t('input.send')}</span>
            <span className="home-send-btn__cell"><EnterGlyph /></span>
          </button>
        )}
      </div>
      {!anyFile && <input ref={imageRef} type="file" multiple accept="image/*" className="hidden" onChange={(e) => { attach('image', e.target) }} />}
      {!anyFile && <input ref={audioRef} type="file" multiple accept="audio/*" className="hidden" onChange={(e) => { attach('audio', e.target) }} />}
      <input ref={fileRef} type="file" multiple accept={anyFile ? undefined : fileAccept} className="hidden" data-testid={fileTestId} onChange={(e) => { attach('file', e.target) }} />
    </form>
  )
}
