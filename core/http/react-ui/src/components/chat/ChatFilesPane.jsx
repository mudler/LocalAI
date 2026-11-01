/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import Button from '../ui/Button'
import PreviewBody, { TEXT_LIMIT } from '../files/PreviewBody'
import { filesApi } from '../../utils/api'
import { fileIcon, fileSize } from '../../utils/fileInfo'
import { groupChatFiles } from '../../hooks/useChatFiles'
import { wellKnownTag } from '../../utils/changeNotes'
import { DOCS_MAKE_YOURS } from '../../utils/discovery'
import { useArrivals } from '../../hooks/useArrivals'
import { copyToClipboard } from '../../utils/clipboard'
import { TEXT_VIEWS, extensionForLanguage, viewOf } from '../../utils/previewPane'
// The rows of the list are the Files page's rows.
import '../../pages/workspace.css'

// fresh: the file arrived while the pane was open ('on', then 'fade': an
// accent wash that fades). recent: the assistant wrote it in the last turn (a 2 px accent edge).
function FileRow({ file, selected, fresh, recent, onSelect, t, tw }) {
  const tag = wellKnownTag(file.path)
  return (
    <li
      className={`ws-row cx-files__row${selected ? ' ws-row--selected' : ''}`}
      data-testid="chat-file-row" data-origin={file.origin} data-path={file.path} data-kind={file.kind || undefined}
      data-fresh={fresh} data-recent={recent || undefined}
    >
      <Icon name={fileIcon(file.name, file.mime)} className="ws-row__icon" />
      <div className="cx-files__main">
        <button
          type="button" className="cx-files__name" onClick={() => onSelect(selected ? null : file)}
          aria-expanded={selected} aria-controls={selected ? 'cx-pv' : undefined} title={file.guest_path}
        >
          {file.name}
        </button>
        <span className="cx-files__meta">
          <span>{fileSize(file.size)}</span>
          {file.turn > 0 && <span>{t('files.turn', { n: file.turn })}</span>}
          {file.media && <span className="cx-files__tag" title={t('files.mediaTitle')}>{t('files.media')}</span>}
          {file.changed && <span className="cx-files__tag" title={t('files.changedTitle')}>{t('files.changedTag')}</span>}
          {tag && <span className="cx-files__tag" data-testid="chat-file-wellknown">{tw(`discovery.files.tag.${tag}`)}</span>}
        </span>
      </div>
      <a
        className="ws-icon-btn" href={filesApi.contentUrl(file.path)} download={file.name}
        aria-label={t('files.download', { name: file.name })} title={t('files.download', { name: file.name })}
      >
        <Icon name="download" />
      </a>
    </li>
  )
}

// The file a step of the transcript names: by its path, as the pane or the
// model knows it, or else by its name.
export function findChatFile(files, path) {
  if (!path) return null
  const name = path.split('/').pop()
  return files.find(f => f.path === path || f.guest_path === path)
    || files.find(f => f.name === name)
    || null
}

const folderOf = (path) => {
  const i = String(path || '').lastIndexOf('/')
  return i > 0 ? path.slice(0, i) : '/'
}

// The selected item on top of the pane: its name, what it is, the actions
// (Download, Copy for text, Open in Files) and its preview.
function SelectedPreview({ item, file, focusToken, t }) {
  const headRef = useRef(null)
  const [copied, setCopied] = useState(false)
  useEffect(() => { setCopied(false) }, [item])
  // A request of the person (a chip, a button) moves the focus here; the
  // pane opening by itself never does.
  useEffect(() => {
    if (focusToken > 0) headRef.current?.focus({ preventScroll: true })
  }, [focusToken])
  useEffect(() => {
    if (!copied) return undefined
    const id = setTimeout(() => setCopied(false), 2000)
    return () => clearTimeout(id)
  }, [copied])
  const isFile = item.type === 'file'
  const view = isFile ? viewOf(item.name, item.mime) : item.view
  const canCopy = !isFile || (TEXT_VIEWS.has(view) && (item.size || 0) <= TEXT_LIMIT)
  const copy = async () => {
    try {
      const text = isFile ? await filesApi.readText(item.path, TEXT_LIMIT) : item.code
      setCopied(await copyToClipboard(text))
    } catch {
      setCopied(false)
    }
  }
  const downloadBlock = () => {
    const blob = new Blob([item.code || ''], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = /\.[a-z0-9]+$/i.test(item.name) ? item.name : `${item.name}.${extensionForLanguage(item.lang)}`
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 0)
  }
  const meta = isFile
    ? [fileSize(item.size || 0), file?.turn > 0 ? t('files.turn', { n: file.turn }) : '', file && file.origin !== 'user' ? (file.changed ? t('preview.changed') : t('preview.created')) : (file ? t('preview.yours') : '')].filter(Boolean)
    : [t('preview.fromReply')]
  return (
    <section className="cx-pv" id="cx-pv" aria-labelledby="cx-pv-name" data-testid="file-preview" data-type={item.type}>
      <div className="cx-pv__head">
        <Icon name={isFile ? fileIcon(item.name, item.mime || '') : 'file-code'} className="cx-pv__icon" />
        <div className="cx-pv__title">
          <h3 className="cx-pv__name" id="cx-pv-name" ref={headRef} tabIndex={-1} title={item.name}>{item.name}</h3>
          <p className="cx-pv__meta">{meta.join(' · ')}</p>
        </div>
      </div>
      <div className="cx-pv__acts" role="group" aria-label={t('preview.actions')}>
        {isFile ? (
          <Button size="sm" icon="download" href={filesApi.contentUrl(item.path)} download={item.name} data-testid="chat-preview-download">{t('preview.download')}</Button>
        ) : (
          <Button size="sm" icon="download" onClick={downloadBlock} data-testid="chat-preview-download">{t('preview.download')}</Button>
        )}
        {canCopy && (
          <Button size="sm" variant="ghost" icon={copied ? 'check' : 'copy'} onClick={copy} data-testid="chat-preview-copy" aria-live="polite">
            {copied ? t('preview.copied') : t('preview.copy')}
          </Button>
        )}
        {isFile && (
          <Button size="sm" variant="ghost" icon="folder" to={`/app/files?path=${encodeURIComponent(folderOf(item.path))}`} data-testid="chat-preview-files">{t('preview.openInFiles')}</Button>
        )}
      </div>
      {/* Keyed by the item, so a new file starts from its own loading state. */}
      <PreviewBody key={isFile ? `f:${item.path}:${item.size}` : `b:${item.id}`} item={item} />
    </section>
  )
}

// The preview pane of a chat: the selected file or code block on top, then
// the chat's files in two groups, what the assistant created or changed and
// what the person gave it, each with its turn. The working files (scripts,
// logs, drafts the assistant made on the way; the server marks them) come
// last, folded into one quiet line until the person opens it. Files dropped on it are added
// to the message being written.
//
// 360 to 480 px docked beside the thread from 1280 px; below that it covers
// the thread from the right with a scrim, and on a phone it is a bottom sheet.
//
// focus asks the pane to show an item: { path, n, by } for a file, or
// { block, n, by } for a code block of a reply (an item of blocks). A new n
// asks again. by 'user' (a chip or a button) moves the focus to the preview;
// 'auto' (the turn produced it) never moves the focus.
export default function ChatFilesPane({ state, chatId = '', focus, blocks = [], onClose, onDropFiles }) {
  const { t } = useTranslation('chat')
  const { t: tw } = useTranslation('workspace')
  const [selected, setSelected] = useState(null)
  const [dragging, setDragging] = useState(false)
  const bodyRef = useRef(null)
  const [focusToken, setFocusToken] = useState(0)
  const [showWorking, setShowWorking] = useState(false)
  const groups = groupChatFiles(state.files)
  const sections = [
    { key: 'created', files: [...groups.created, ...groups.changed] },
    { key: 'uploaded', files: groups.uploaded },
  ]
  const fresh = useArrivals(state.files.map(f => f.path), { scope: chatId, ready: state.loaded })
  const lastTurn = state.files.reduce((n, f) => Math.max(n, f.turn || 0), 0)
  // Keep the preview on the same file when the list is read again, and drop
  // it when the file is gone.
  const currentFile = selected?.type === 'file' ? state.files.find(f => f.path === selected.path) : null
  const currentBlock = selected?.type === 'block' ? blocks.find(b => b.id === selected.id) : null
  const current = currentFile ? { type: 'file', path: currentFile.path, name: currentFile.name, size: currentFile.size, mime: currentFile.mime || '' } : currentBlock
  useEffect(() => {
    if (selected?.type === 'file' && state.loaded && !currentFile) setSelected(null)
  }, [selected, currentFile, state.loaded])
  // A working file opened from a step shows its row: the group unfolds.
  const selectedWorking = groups.working.some(f => f.path === currentFile?.path)
  useEffect(() => {
    if (selectedWorking) setShowWorking(true)
  }, [selectedWorking])

  const focusPath = focus?.path
  const focusBlock = focus?.block
  const focusN = focus?.n
  const focusBy = focus?.by
  useEffect(() => {
    let next = null
    if (focusPath) {
      const file = findChatFile(state.files, focusPath)
      if (file) next = { type: 'file', path: file.path }
    } else if (focusBlock && blocks.some(b => b.id === focusBlock)) {
      next = { type: 'block', id: focusBlock }
    }
    if (!next) return
    setSelected(next)
    bodyRef.current?.scrollTo?.({ top: 0 })
    if (focusBy === 'user') setFocusToken(n => n + 1)
  // The list is read again while a turn runs; only a new request moves the selection.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusPath, focusBlock, focusN, state.files.length > 0, blocks.length > 0])

  const select = (file) => setSelected(file ? { type: 'file', path: file.path } : null)
  const onDrop = (e) => {
    e.preventDefault()
    setDragging(false)
    if (e.dataTransfer?.files?.length) onDropFiles(Array.from(e.dataTransfer.files))
  }
  return (
    <>
    <div className="cx-files-scrim" onMouseDown={onClose} aria-hidden="true" />
    <aside
      className={`cx-files${dragging ? ' cx-files--drop' : ''}${current ? ' cx-files--previewing' : ''}`}
      aria-label={t('files.title')}
      id="chat-files-pane"
      data-testid="chat-files-pane"
      onDragOver={(e) => { e.preventDefault(); setDragging(true) }}
      onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) setDragging(false) }}
      onDrop={onDrop}
    >
      <span className="cx-files__grip" aria-hidden="true" />
      <div className="cx-files__head">
        <h2 className="cx-files__title">
          {t('files.title')}
          {state.files.length > 0 && <span className="cx-files__count">{state.files.length}</span>}
        </h2>
        <Button variant="ghost" size="sm" icon="close" onClick={onClose} title={t('files.close')} aria-label={t('files.close')} data-testid="chat-files-close" />
      </div>
      <div className="cx-files__body" ref={bodyRef}>
        {current && <SelectedPreview item={current} file={currentFile} focusToken={focusToken} t={t} />}
        {state.error && <p className="ws-error" role="alert">{t('files.error')}</p>}
        {state.folderPath && (
          <p className="cx-files__folder" title={state.folder}>
            <Icon name="folder" /> <span>{state.folderPath}</span>
          </p>
        )}
        {state.loaded && state.files.length === 0 && !state.error && !current && (
          <div className="cx-files__empty" data-testid="chat-files-empty">
            <Icon name="paperclip" />
            <p className="cx-files__empty-title">{t('files.emptyTitle')}</p>
            <p className="ws-muted">{t('files.emptyBody')}</p>
          </div>
        )}
        {sections.filter(s => s.files.length > 0).map(s => (
          <section key={s.key} className="cx-files__group" aria-labelledby={`cx-files-${s.key}`} data-testid={`chat-files-${s.key}`}>
            <h3 className="cx-files__heading" id={`cx-files-${s.key}`}>
              {t(`files.groups.${s.key}`)} <span className="cx-files__count">{s.files.length}</span>
            </h3>
            <ul className="ws-list">
              {s.files.map(f => (
                <FileRow
                  key={f.path} file={f} selected={currentFile?.path === f.path} onSelect={select} t={t} tw={tw}
                  fresh={fresh.get(f.path)} recent={f.origin !== 'user' && lastTurn > 0 && f.turn === lastTurn}
                />
              ))}
            </ul>
          </section>
        ))}
        {groups.working.length > 0 && (
          <section className="cx-files__group cx-files__group--working" aria-labelledby="cx-files-working" data-testid="chat-files-working" data-open={showWorking || undefined}>
            <h3 className="cx-files__heading" id="cx-files-working">
              <button
                type="button" className="cx-files__fold" aria-expanded={showWorking} aria-controls={showWorking ? 'cx-files-working-list' : undefined}
                onClick={() => setShowWorking(v => !v)} data-testid="chat-files-working-toggle"
              >
                <Icon name="chevron-right" className="cx-files__fold-chev" />
                {t('files.groups.working')}
                <span className="cx-files__count">· {groups.working.length}</span>
              </button>
            </h3>
            {showWorking && (
              <>
                <p className="cx-files__fold-note">{t('files.workingNote')}</p>
                <ul className="ws-list" id="cx-files-working-list">
                  {groups.working.map(f => (
                    <FileRow
                      key={f.path} file={f} selected={currentFile?.path === f.path} onSelect={select} t={t} tw={tw}
                      fresh={fresh.get(f.path)} recent={false}
                    />
                  ))}
                </ul>
              </>
            )}
          </section>
        )}
        {state.files.some(f => wellKnownTag(f.path)) && (
          <p className="ws-hint cx-files__hint wk-files-note" data-testid="chat-files-wellknown-note">
            <Icon name="info" /> {tw('discovery.files.learn')}{' '}
            <a href={DOCS_MAKE_YOURS} target="_blank" rel="noopener noreferrer">{tw('discovery.ex.learn')}</a>
          </p>
        )}
        {state.files.length > 0 && <p className="ws-hint cx-files__hint">{t('files.dropHint')}</p>}
      </div>
    </aside>
    </>
  )
}
