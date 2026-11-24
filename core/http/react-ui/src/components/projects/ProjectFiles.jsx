/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { Button, Callout, LoadError, SkeletonRows } from '../ui'
import { EntryIcon } from '../files/FileList'
import FilePreview from '../files/FilePreview'
import UploadTray from '../files/UploadTray'
import { useWorkspace, joinPath, parentOf } from '../../hooks/useWorkspace'
import { uploads, useUploads } from '../../hooks/useUploads'
import { filesOfDrop, hasFiles } from '../../utils/dropFiles'
import { relativeDate, sortEntries, uniqueName } from '../../utils/fileBrowser'
import { fileSize } from '../../utils/fileInfo'
import { projectFilesPath } from '../../utils/projects'

// The Files tab: the files of the project's files/ folder, with a place to drop
// or upload more. What is in this folder is what the project's chats and agents
// can use. An archived project is read-only here, as it is on the Tool defaults
// tab: the files stay listed and open, and nothing can be added. It reuses the Files page parts (the list icon, the preview pane, the
// upload queue and tray) and sends a folder to the Files page.
export default function ProjectFiles({ project, archived = false }) {
  const { t, i18n } = useTranslation('projects')
  const { t: tw } = useTranslation('workspace')
  const path = projectFilesPath(project)
  const ws = useWorkspace(path)
  const upState = useUploads()
  const fileInput = useRef(null)
  const depth = useRef(0)
  const [over, setOver] = useState(false)
  const [previewPath, setPreviewPath] = useState(null)

  // An upload that ends in this folder shows at once.
  useEffect(() => uploads.onSettled((item) => {
    if (item.status === 'done' && item.dir === path) ws.refresh(undefined, { quiet: true })
  }), [path])  // eslint-disable-line react-hooks/exhaustive-deps

  const queue = useCallback((list) => {
    const limit = ws.maxFileSize
    const items = list.map(x => ({
      file: x.file,
      name: x.rel.split('/').pop(),
      path: joinPath(path, x.rel),
      dir: parentOf(joinPath(path, x.rel)),
      mode: 'keep',
      error: limit > 0 && x.file.size > limit ? { key: 'uploads.errors.tooBig', limit, retry: false } : null,
    }))
    if (items.length) uploads.add(items)
  }, [path, ws.maxFileSize])

  const pick = (e) => {
    const list = Array.from(e.target.files || []).map(file => ({ file, rel: file.name }))
    e.target.value = ''
    queue(list)
  }
  const onDrop = async (e) => {
    if (!hasFiles(e.dataTransfer)) return
    e.preventDefault()
    depth.current = 0
    setOver(false)
    if (archived) return
    queue(await filesOfDrop(e.dataTransfer))
  }
  const onDragOver = (e) => { if (!archived && hasFiles(e.dataTransfer)) e.preventDefault() }
  const onDragEnter = (e) => { if (!archived && hasFiles(e.dataTransfer)) { depth.current += 1; setOver(true) } }
  const onDragLeave = (e) => { if (hasFiles(e.dataTransfer)) { depth.current = Math.max(0, depth.current - 1); if (depth.current === 0) setOver(false) } }

  const newFile = async () => {
    const taken = ws.entries.map(x => x.name)
    const node = await ws.touch(uniqueName(tw('newFileName'), taken))
    if (node?.path) setPreviewPath(node.path)
  }

  const previewing = ws.entries.find(x => x.path === previewPath && x.kind === 'file')
  const empty = ws.loaded && ws.entries.length === 0
  const folderLink = `/app/files?path=${encodeURIComponent(path)}`

  return (
    <section className="pj-section" data-testid="project-files">
      <div className="pj-head">
        <h2 className="pj-h2">{t('files.title')}</h2>
        {!archived && (
          <div className="pj-files__acts">
            <Button size="sm" variant="ghost" icon="plus" onClick={newFile} disabled={ws.busy} data-testid="project-files-new">{t('files.newFile')}</Button>
            <Button size="sm" variant="primary" icon="upload" onClick={() => fileInput.current?.click()} data-testid="project-files-upload">{t('files.upload')}</Button>
            <input ref={fileInput} type="file" multiple hidden data-testid="project-files-input" aria-label={t('files.upload')} onChange={pick} />
          </div>
        )}
      </div>
      {archived
        ? <Callout tone="info" testId="project-files-archived">{t('files.archivedNote')}</Callout>
        : <p className="pj-note" data-testid="project-files-note">{t('files.body')}</p>}

      {ws.error && <Callout tone="error" role="alert" testId="project-files-problem">{ws.error}</Callout>}

      <div
        className={`pj-drop${over ? ' pj-drop--over' : ''}`} data-testid="project-files-drop"
        onDragEnter={onDragEnter} onDragOver={onDragOver} onDragLeave={onDragLeave} onDrop={onDrop}
      >
        {!archived && <p className="pj-drop__title">{over ? t('files.dropOver') : t('files.dropTitle')}</p>}
        <p className="pj-mono pj-drop__path" data-testid="project-files-path">{path.slice(1)}</p>
        {ws.loadError && !ws.loaded ? (
          <LoadError title={t('files.loadFailed')} error={ws.loadError} onRetry={() => ws.refresh()} testId="project-files-error" />
        ) : ws.loading && !ws.loaded ? (
          <SkeletonRows count={3} label={t('files.loading')} />
        ) : empty ? (
          <p className="pj-note" data-testid="project-files-empty">{archived ? t('files.emptyArchived') : t('files.empty')}</p>
        ) : (
          <ul className="pj-files" data-testid="project-files-list">
            {sortEntries(ws.entries).map((e) => {
              const isDir = e.kind === 'dir'
              const when = relativeDate(e.modified, { locale: i18n.language })
              return (
                <li key={e.path} className="pj-file" data-testid="project-file" data-path={e.path}>
                  <EntryIcon entry={e} />
                  {isDir ? (
                    <Link to={`/app/files?path=${encodeURIComponent(e.path)}`} className="pj-file__name" aria-label={t('files.openFile', { name: e.name })}>{e.name}</Link>
                  ) : (
                    <button type="button" className="pj-file__name" onClick={() => setPreviewPath(e.path)} aria-label={t('files.openFile', { name: e.name })}>{e.name}</button>
                  )}
                  <span className="pj-file__meta" title={when.exact}>{[isDir ? '' : fileSize(e.size), when.label].filter(Boolean).join(' · ')}</span>
                </li>
              )
            })}
          </ul>
        )}
      </div>

      <p className="pj-note">
        <Button variant="ghost" size="sm" icon="folder" to={folderLink} data-testid="project-files-open">{t('files.open')}</Button>
      </p>

      {previewing && (
        <FilePreview entry={previewing} onClose={() => setPreviewPath(null)} />
      )}
      <UploadTray state={upState} queue={uploads} onOpen={(item) => setPreviewPath(item.result?.path || null)} />
    </section>
  )
}
