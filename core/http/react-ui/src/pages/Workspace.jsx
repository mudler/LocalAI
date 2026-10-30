/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
import Icon from '../components/Icon'
import { useWorkspace, joinPath, parentOf } from '../hooks/useWorkspace'
import { filesApi } from '../utils/api'
import FilePreview from '../components/files/FilePreview'
import SharedFolders from '../components/files/SharedFolders'
import ConfirmDialog from '../components/ConfirmDialog'
import { fileSize as size } from '../utils/fileInfo'
import { splitEntries, isSettingsFolder, guardOf } from '../utils/managedFiles'
import './workspace.css'

function Breadcrumb({ path, onOpen, t }) {
  const parts = path.split('/').filter(Boolean)
  return (
    <nav className="ws-crumbs" aria-label={t('breadcrumb')}>
      <button type="button" className="ws-link" onClick={() => onOpen('/')}>{t('home')}</button>
      {parts.map((p, i) => {
        const to = `/${parts.slice(0, i + 1).join('/')}`
        return (
          <span key={to}>
            <span className="ws-crumbs__sep" aria-hidden="true">/</span>
            <button type="button" className="ws-link" onClick={() => onOpen(to)}>{p}</button>
          </span>
        )
      })}
    </nav>
  )
}

// EntryRow is one file or folder. onRename and onRemove go through the
// page, which asks first for a folder or a file that LocalAI manages.
function EntryRow({ entry, selected, onSelect, onOpen, onRename, onRemove, t }) {
  const [renaming, setRenaming] = useState(null)
  const isDir = entry.kind === 'dir'
  const finish = async (commit) => {
    const name = (renaming || '').trim()
    setRenaming(null)
    if (commit && name && name !== entry.name) await onRename(entry, name)
  }
  const about = isDir && entry.managed ? t(`managed.about.${entry.managed}`, { defaultValue: '' }) : ''
  return (
    <li
      className={`ws-row${selected ? ' ws-row--selected' : ''}${entry.managed ? ' ws-row--managed' : ''}`}
      data-testid="ws-row" data-managed={entry.managed || undefined}
    >
      <Icon name={isDir ? 'folder' : 'file'} className="ws-row__icon" />
      {renaming === null ? (
        <span className="ws-row__main">
          <button type="button" className="ws-row__name" onClick={() => (isDir ? onOpen(entry.path) : onSelect(entry))}>
            {entry.name}{isDir ? '/' : ''}
          </button>
          {isDir && entry.managed && <span className="ws-managed-tag" data-testid="ws-managed-tag">{t('managed.marker')}</span>}
          {about && <span className="ws-row__about">{about}</span>}
        </span>
      ) : (
        <input
          className="input ws-row__edit" aria-label={t('rename')} value={renaming} autoFocus
          onChange={(e) => setRenaming(e.target.value)} onBlur={() => finish(true)}
          // preventDefault: the Enter must not also press the confirmation
          // that a rename of a managed path opens and focuses.
          onKeyDown={(e) => {
            if (e.key === 'Enter') { e.preventDefault(); finish(true) }
            if (e.key === 'Escape') finish(false)
          }}
        />
      )}
      <span className="ws-row__size">{isDir ? '' : size(entry.size)}</span>
      <span className="ws-row__actions">
        {!isDir && (
          <a className="ws-icon-btn" href={filesApi.contentUrl(entry.path)} download={entry.name} aria-label={t('download')} title={t('download')}>
            <Icon name="download" />
          </a>
        )}
        <button type="button" className="ws-icon-btn" aria-label={t('rename')} title={t('rename')} onClick={() => setRenaming(entry.name)}>
          <Icon name="pencil" />
        </button>
        <button type="button" className="ws-icon-btn" aria-label={t('delete')} title={t('delete')} onClick={() => onRemove(entry)}>
          <Icon name="trash" />
        </button>
      </span>
    </li>
  )
}

function TrashList({ ws, t }) {
  if (ws.trash.length === 0) return <p className="ws-empty">{t('trash.empty')}</p>
  return (
    <ul className="ws-list" aria-label={t('tabs.trash')}>
      {ws.trash.map((e) => (
        <li key={e.rev} className="ws-row" data-testid="ws-trash-row">
          <Icon name="trash" className="ws-row__icon" />
          <span className="ws-row__name ws-row__name--static">{e.paths.join(', ')}</span>
          <span className="ws-row__size">{new Date(e.at).toLocaleString()}</span>
          <span className="ws-row__actions">
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => ws.restore(e.rev)}>
              <Icon name="undo" /> {t('trash.restore')}
            </button>
          </span>
        </li>
      ))}
    </ul>
  )
}

function HistoryList({ ws, t }) {
  useEffect(() => { ws.loadHistory() }, [ws.path])  // eslint-disable-line react-hooks/exhaustive-deps
  if (ws.history.length === 0) return <p className="ws-empty">{t('history.empty')}</p>
  return (
    <ul className="ws-list" aria-label={t('tabs.history')}>
      {ws.history.map((h) => (
        <li key={h.rev} className="ws-row" data-testid="ws-history-row">
          <Icon name="history" className="ws-row__icon" />
          <span className="ws-row__name ws-row__name--static">
            {h.title}
            {h.kind === 'agent' && <span className="ws-tag">{t('history.agent')}</span>}
          </span>
          <span className="ws-row__size">{new Date(h.at).toLocaleString()}</span>
          <span className="ws-row__actions">
            {h.kind !== 'undo' && (
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => ws.restore(h.rev)}>
                <Icon name="undo" /> {t('history.undo')}
              </button>
            )}
          </span>
        </li>
      ))}
    </ul>
  )
}

export default function Workspace() {
  const { t } = useTranslation('workspace')
  // ?path= opens a folder, for example from "Show its files" of a page.
  const [params] = useSearchParams()
  const ws = useWorkspace(params.get('path') || '/')
  const [tab, setTab] = useState(params.get('tab') === 'shared' ? 'shared' : 'files')
  const [selected, setSelected] = useState(null)
  const [dragging, setDragging] = useState(false)
  const [folderName, setFolderName] = useState(null)
  const [query, setQuery] = useState('')
  // A delete or a rename of a managed path that waits for the confirmation:
  // { entry, action: 'delete' | 'rename', name, guard }.
  const [guarded, setGuarded] = useState(null)
  const fileInput = useRef(null)

  const remove = (entry) => {
    const guard = guardOf(entry)
    if (guard) setGuarded({ entry, action: 'delete', guard })
    else ws.remove(entry)
  }
  const rename = (entry, name) => {
    const guard = guardOf(entry)
    if (guard) setGuarded({ entry, action: 'rename', name, guard })
    else return ws.rename(entry, name)
  }
  const confirmGuarded = async () => {
    const g = guarded
    setGuarded(null)
    if (!g) return
    if (g.action === 'delete') await ws.remove(g.entry)
    else await ws.rename(g.entry, g.name)
  }
  const { own, managed } = splitEntries(ws.entries, ws.path)
  const row = (e) => (
    <EntryRow
      key={e.path} entry={e} t={t} selected={selected?.path === e.path}
      onSelect={setSelected} onOpen={openFolder} onRename={rename} onRemove={remove}
    />
  )

  const onDrop = (e) => {
    e.preventDefault()
    setDragging(false)
    if (e.dataTransfer?.files?.length) ws.upload(e.dataTransfer.files)
  }
  const openFolder = (p) => { setSelected(null); setTab('files'); ws.open(p) }
  const makeFolder = async () => {
    const name = (folderName || '').trim()
    setFolderName(null)
    if (name) await ws.mkdir(name)
  }

  if (ws.off) {
    return (
      <div className="ws-page" data-testid="workspace-page">
        <div className="ws-main">
          <h1 className="ws-title">{t('title')}</h1>
          <div className="ws-notice" role="note" data-testid="files-off">{ws.off}</div>
        </div>
      </div>
    )
  }

  return (
    <div className="ws-page" data-testid="workspace-page">
      <div className="ws-main">
        <header className="ws-head">
          <h1 className="ws-title">{t('title')}</h1>
          <form className="ws-search" role="search" onSubmit={(e) => { e.preventDefault(); ws.search(query) }}>
            <input
              className="input" type="search" aria-label={t('search.label')} placeholder={t('search.placeholder')}
              value={query} onChange={(e) => { setQuery(e.target.value); if (!e.target.value) ws.clearSearch() }}
            />
          </form>
          <div className="ws-head__actions">
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => setFolderName('')}>
              <Icon name="plus" /> {t('newFolder')}
            </button>
            <button type="button" className="btn btn-primary btn-sm" onClick={() => fileInput.current?.click()}>
              <Icon name="upload" /> {t('upload')}
            </button>
            <input
              ref={fileInput} type="file" multiple hidden data-testid="ws-file-input" aria-label={t('upload')}
              onChange={(e) => { if (e.target.files?.length) ws.upload(e.target.files); e.target.value = '' }}
            />
          </div>
        </header>

        {ws.error && (
          <div className="ws-error" role="alert">
            <span>{ws.error}</span>
            <button type="button" className="ws-link" onClick={ws.clearError}>{t('dismiss')}</button>
          </div>
        )}

        {ws.hits !== null ? (
          <section aria-label={t('search.results')}>
            <h2 className="ws-sub">{t('search.results')}</h2>
            {ws.hits.length === 0 ? <p className="ws-empty">{t('search.none')}</p> : (
              <ul className="ws-list">
                {ws.hits.map((h, i) => (
                  <li key={`${h.path}:${h.line}:${i}`} className="ws-row" data-testid="ws-hit">
                    <Icon name="file" className="ws-row__icon" />
                    <button type="button" className="ws-row__name" onClick={() => { ws.clearSearch(); setQuery(''); openFolder(parentOf(h.path)) }}>
                      {h.path}{h.line ? `:${h.line}` : ''}
                    </button>
                    <span className="ws-row__size ws-snippet">{h.snippet}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        ) : (
          <>
            <div className="ws-tabs" role="tablist" aria-label={t('tabs.label')}>
              {['files', 'trash', 'history', 'shared'].map((k) => (
                <button
                  key={k} type="button" role="tab" aria-selected={tab === k} id={`ws-tab-${k}`}
                  className={`ws-tab${tab === k ? ' ws-tab--on' : ''}`} onClick={() => setTab(k)}
                >
                  {t(`tabs.${k}`)}{k === 'trash' && ws.trash.length > 0 ? ` (${ws.trash.length})` : ''}
                </button>
              ))}
            </div>

            {tab === 'files' && (
              <section
                className={`ws-files${dragging ? ' ws-files--drop' : ''}`} aria-labelledby="ws-tab-files"
                onDragOver={(e) => { e.preventDefault(); setDragging(true) }} onDragLeave={() => setDragging(false)} onDrop={onDrop}
              >
                <Breadcrumb path={ws.path} onOpen={openFolder} t={t} />
                {folderName !== null && (
                  <div className="ws-newfolder">
                    <input
                      className="input" aria-label={t('folderName')} placeholder={t('folderName')} value={folderName} autoFocus
                      onChange={(e) => setFolderName(e.target.value)}
                      onKeyDown={(e) => { if (e.key === 'Enter') makeFolder(); if (e.key === 'Escape') setFolderName(null) }}
                    />
                    <button type="button" className="btn btn-primary btn-sm" onClick={makeFolder}>{t('create')}</button>
                  </div>
                )}
                {ws.path !== '/' && (
                  <button type="button" className="ws-up ws-link" onClick={() => openFolder(parentOf(ws.path))}>
                    <Icon name="arrow-up" /> {t('up')}
                  </button>
                )}
                {isSettingsFolder(ws.path) && (
                  <p className="ws-folder-about" data-testid="ws-folder-about">
                    <span className="ws-managed-tag">{t('managed.marker')}</span> {t('managed.about.localai')}
                  </p>
                )}
                {ws.entries.length === 0 && !ws.loading ? (
                  <p className="ws-empty">{t('empty')}</p>
                ) : (
                  <>
                    {own.length > 0 && (
                      <ul className="ws-list" aria-label={t('tabs.files')}>
                        {own.map(row)}
                      </ul>
                    )}
                    {managed.length > 0 && (
                      <section className="ws-managed" aria-labelledby="ws-managed-title" data-testid="ws-managed">
                        <h2 className="ws-managed__title" id="ws-managed-title">{t('managed.group')}</h2>
                        <ul className="ws-list" aria-labelledby="ws-managed-title">
                          {managed.map(row)}
                        </ul>
                      </section>
                    )}
                  </>
                )}
                <p className="ws-hint">{t('dropHint')}</p>
              </section>
            )}
            {tab === 'trash' && <TrashList ws={ws} t={t} />}
            {tab === 'history' && <HistoryList ws={ws} t={t} />}
            {tab === 'shared' && <SharedFolders />}
          </>
        )}
      </div>
      {selected && tab === 'files' && ws.hits === null && <FilePreview entry={selected} t={t} />}
      <ConfirmDialog
        open={!!guarded} danger
        title={guarded ? t(guarded.action === 'delete' ? 'managed.confirm.deleteTitle' : 'managed.confirm.renameTitle', { name: guarded.guard.name }) : ''}
        message={guarded ? `${t(`managed.loses.${guarded.guard.what}`)} ${t(guarded.action === 'delete' ? 'managed.confirm.restore' : 'managed.confirm.renameNote')}` : ''}
        confirmLabel={guarded ? t(guarded.action === 'delete' ? 'managed.confirm.delete' : 'managed.confirm.rename') : ''}
        onConfirm={confirmGuarded} onCancel={() => setGuarded(null)}
      />
    </div>
  )
}
