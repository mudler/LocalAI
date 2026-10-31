/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useOutletContext, useSearchParams } from 'react-router-dom'
import Icon from '../components/Icon'
import Button from '../components/ui/Button'
import Callout from '../components/ui/Callout'
import EmptyState from '../components/ui/EmptyState'
import LoadError from '../components/ui/LoadError'
import { SkeletonRows } from '../components/Skeleton'
import ActionMenu from '../components/ActionMenu'
import ConfirmDialog from '../components/ConfirmDialog'
import { useAuth } from '../context/AuthContext'
import { useWorkspace, joinPath, parentOf } from '../hooks/useWorkspace'
import { uploads, useUploads } from '../hooks/useUploads'
import { filesApi } from '../utils/api'
import { fileSize } from '../utils/fileInfo'
import { splitEntries, isSettingsFolder, guardOf } from '../utils/managedFiles'
import {
  DRAG_TYPE, filterEntries, parseViewPrefs, pastedName, rangeOf, sortEntries, uniqueName,
} from '../utils/fileBrowser'
import { filesOfDrop, hasFiles } from '../utils/dropFiles'
import FilePreview from '../components/files/FilePreview'
import FileList, { EntryIcon } from '../components/files/FileList'
import UploadTray from '../components/files/UploadTray'
import ConflictDialog from '../components/files/ConflictDialog'
import MoveDialog from '../components/files/MoveDialog'
import SharedFolders from '../components/files/SharedFolders'
import './workspace.css'

const noop = () => {}

// After a menu closes it gives the focus back to its trigger on the next
// frame; a rename field opened from the menu must take the focus after that.
const afterMenu = (fn) => requestAnimationFrame(() => requestAnimationFrame(fn))

// The view, sort column and direction, kept per user in this browser.
function useViewPrefs(userKey) {
  const key = `localai-files-view:${userKey}`
  const [prefs, setPrefs] = useState(() => {
    try { return parseViewPrefs(localStorage.getItem(key)) } catch { return parseViewPrefs('') }
  })
  const update = useCallback((next) => {
    setPrefs(prev => {
      const merged = { ...prev, ...next }
      try { localStorage.setItem(key, JSON.stringify(merged)) } catch { /* private mode: the choice lasts the visit */ }
      return merged
    })
  }, [key])
  return [prefs, update]
}

// The folder path as crumbs. A deep path folds its middle into a menu, so
// the bar stays on one line; each crumb takes rows dropped on it.
function Breadcrumb({ path, onOpen, drop, t }) {
  const parts = path.split('/').filter(Boolean)
  const crumbs = parts.map((p, i) => ({ name: p, to: `/${parts.slice(0, i + 1).join('/')}` }))
  const folded = crumbs.length > 3 ? crumbs.slice(0, crumbs.length - 2) : []
  const shown = crumbs.length > 3 ? crumbs.slice(-2) : crumbs
  const crumb = (to, label, current, icon) => (
    current ? (
      <span className="ws-crumb ws-crumb--here" aria-current="page" title={label}>{icon}{label}</span>
    ) : (
      <button
        type="button" className={`ws-crumb${drop.path === to ? ' ws-crumb--drop' : ''}`} title={label} onClick={() => onOpen(to)}
        onDragOver={(e) => drop.over(to, e)} onDragLeave={() => drop.leave(to)} onDrop={(e) => drop.drop(to, e)}
      >
        {icon}{label}
      </button>
    )
  )
  return (
    <nav className="ws-crumbs" aria-label={t('breadcrumb')} data-testid="ws-crumbs">
      {crumb('/', t('home'), parts.length === 0, <Icon name="home" className="ws-crumb__home" />)}
      {folded.length > 0 && (
        <>
          <Icon name="chevron-right" className="ws-crumbs__sep" />
          <span className="ws-crumbs__fold">
            <ActionMenu
              compact icon="more" ariaLabel={t('crumbs.hidden')} triggerLabel={t('crumbs.hidden')}
              items={folded.map(c => ({ key: c.to, icon: 'folder', label: c.name, onClick: () => onOpen(c.to) }))}
            />
          </span>
        </>
      )}
      {shown.map((c, i) => (
        <span key={c.to} className="ws-crumbs__step">
          <Icon name="chevron-right" className="ws-crumbs__sep" />
          {crumb(c.to, c.name, i === shown.length - 1)}
        </span>
      ))}
    </nav>
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

// The results of a search across every file: the path, the line and the
// text around the match. A hit opens its folder with the file in preview.
function SearchHits({ hits, query, onOpenHit, onClear, t }) {
  return (
    <section className="ws-hits" aria-labelledby="ws-hits-title">
      <div className="ws-hits__head">
        <h2 className="ws-sub" id="ws-hits-title">{t('search.resultsFor', { query })}</h2>
        <Button size="sm" variant="ghost" icon="close" onClick={onClear}>{t('search.back')}</Button>
      </div>
      {hits.length === 0 ? (
        <EmptyState variant="no-results" title={t('search.none')} body={t('search.noneBody')} level={3} />
      ) : (
        <ul className="ws-hits__list">
          {hits.map((h, i) => (
            <li key={`${h.path}:${h.line}:${i}`} data-testid="ws-hit">
              <button type="button" className="ws-hit" onClick={() => onOpenHit(h)}>
                <EntryIcon entry={{ kind: 'file', name: h.path.split('/').pop(), path: h.path, size: Infinity }} />
                <span className="ws-hit__main">
                  <span className="ws-hit__path">{h.path}{h.line ? <span className="ws-hit__line">:{h.line}</span> : null}</span>
                  {h.snippet && <span className="ws-hit__snippet">{h.snippet}</span>}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

export default function Workspace() {
  const { t, i18n } = useTranslation('workspace')
  const { addToast = noop } = useOutletContext() || {}
  const { user } = useAuth()
  // ?path= opens a folder, for example from "Show its files" of a page.
  const [params, setParams] = useSearchParams()
  const urlPath = params.get('path') || '/'
  const ws = useWorkspace(urlPath)
  const upState = useUploads()
  const [tab, setTab] = useState(params.get('tab') === 'shared' ? 'shared' : 'files')
  const [prefs, setPrefs] = useViewPrefs(user?.id || 'local')
  const [query, setQuery] = useState('')
  const [searched, setSearched] = useState('')
  const [selected, setSelected] = useState(() => new Set())
  const [anchor, setAnchor] = useState(null)
  const [cursor, setCursor] = useState(null)
  const [previewPath, setPreviewPath] = useState(null)
  const [renaming, setRenaming] = useState(null)
  const [creating, setCreating] = useState(null)
  const [menu, setMenu] = useState(null)
  const [moving, setMoving] = useState(null)
  const [conflict, setConflict] = useState(null)
  const [dropActive, setDropActive] = useState(false)
  const [dropPath, setDropPath] = useState(null)
  const [dragCount, setDragCount] = useState(0)
  // A delete or a rename of a managed path that waits for the confirmation:
  // { entries, action: 'delete' | 'rename', name, guard }.
  const [guarded, setGuarded] = useState(null)
  const fileInput = useRef(null)
  const folderInput = useRef(null)
  const searchRef = useRef(null)
  const chipRef = useRef(null)
  const dragDepth = useRef(0)
  const dragged = useRef(null)
  const now = Date.now()
  const touch = typeof window !== 'undefined' && !!window.matchMedia?.('(pointer: coarse)').matches

  // The URL follows the open folder, so Back goes to the folder before.
  useEffect(() => {
    if (urlPath !== ws.path) ws.open(urlPath)
  }, [urlPath])  // eslint-disable-line react-hooks/exhaustive-deps

  const openFolder = useCallback((p) => {
    setSelected(new Set())
    setPreviewPath(null)
    setRenaming(null)
    setCreating(null)
    setQuery('')
    setSearched('')
    setTab('files')
    const next = new URLSearchParams(params)
    if (p === '/') next.delete('path')
    else next.set('path', p)
    next.delete('tab')
    setParams(next)
    if (p === ws.path) ws.open(p)
  }, [params, setParams, ws])

  // The list as shown: filtered by the query, sorted, the user's own files
  // first and the folders LocalAI manages after them.
  const filtered = useMemo(() => filterEntries(ws.entries, query), [ws.entries, query])
  const { own, managed } = splitEntries(filtered, ws.path)
  const groups = useMemo(() => {
    const g = [{ key: 'own', entries: sortEntries(own, prefs) }]
    if (managed.length) g.push({ key: 'managed', title: t('managed.group'), entries: managed })
    return g
  }, [own, managed, prefs, t])
  const order = groups.flatMap(g => g.entries)
  const byPath = useMemo(() => new Map(ws.entries.map(e => [e.path, e])), [ws.entries])
  const taken = ws.entries.map(e => e.name)
  const preview = previewPath ? byPath.get(previewPath) : null
  const picked = order.filter(e => selected.has(e.path))

  // A selection keeps only what is still in the folder.
  useEffect(() => {
    setSelected(prev => {
      const next = new Set([...prev].filter(p => byPath.has(p)))
      return next.size === prev.size ? prev : next
    })
    if (previewPath && ws.loaded && !byPath.has(previewPath)) setPreviewPath(null)
  }, [byPath])  // eslint-disable-line react-hooks/exhaustive-deps

  // An upload that ends in the open folder shows at once.
  useEffect(() => uploads.onSettled((item) => {
    if (item.status === 'done' && (item.dir === ws.path || item.dir.startsWith(`${ws.path === '/' ? '' : ws.path}/`))) {
      ws.refresh(undefined, { quiet: true })
    }
  }), [ws.path])  // eslint-disable-line react-hooks/exhaustive-deps

  // ---- uploads ---------------------------------------------------------------

  // queueFiles checks the names against the folder, asks what to do with a
  // clash, refuses a file over the server's limit before sending it, and
  // queues the rest. list: [{ file, rel }], rel the path inside the drop.
  const queueFiles = useCallback(async (list, dir = ws.path) => {
    if (!list.length) return
    let names = new Set()
    if (dir === ws.path) names = new Set(ws.entries.filter(e => e.kind === 'file').map(e => e.name.toLowerCase()))
    else {
      try {
        const res = await filesApi.list(dir)
        names = new Set((res.entries || []).filter(e => e.kind === 'file').map(e => e.name.toLowerCase()))
      } catch { /* the server keeps both on a clash it finds itself */ }
    }
    const clashes = list.filter(x => !x.rel.includes('/') && names.has(x.rel.toLowerCase()))
    let choice = 'keep'
    if (clashes.length > 0) {
      choice = await new Promise(resolve => setConflict({
        names: clashes.map(x => x.rel), folder: dir === '/' ? t('home') : dir.split('/').pop(), resolve,
      }))
      setConflict(null)
      if (choice === null) return
    }
    const clash = new Set(clashes.map(x => x.rel))
    const limit = ws.maxFileSize
    const items = list
      .filter(x => !(choice === 'skip' && clash.has(x.rel)))
      .map(x => ({
        file: x.file,
        name: x.rel.split('/').pop(),
        path: joinPath(dir, x.rel),
        dir: parentOf(joinPath(dir, x.rel)),
        mode: choice === 'replace' && clash.has(x.rel) ? 'replace' : 'keep',
        error: limit > 0 && x.file.size > limit ? { key: 'uploads.errors.tooBig', limit, retry: false } : null,
      }))
    if (items.length) uploads.add(items)
  }, [ws.path, ws.entries, ws.maxFileSize, t])

  const pickFiles = (e) => {
    const list = Array.from(e.target.files || []).map(file => ({ file, rel: file.webkitRelativePath || file.name }))
    e.target.value = ''
    queueFiles(list)
  }

  // A paste of files (a screenshot, files copied in a file manager) uploads
  // them to the open folder. A paste into a text field stays a paste.
  useEffect(() => {
    if (tab !== 'files' || ws.off) return undefined
    const onPaste = (e) => {
      const el = e.target
      if (el?.closest?.('input, textarea, [contenteditable="true"], [role="dialog"]')) return
      const files = Array.from(e.clipboardData?.files || [])
      if (!files.length) return
      e.preventDefault()
      const at = new Date()
      queueFiles(files.map(file => {
        const name = pastedName(file.name, at)
        return { file: name === file.name ? file : new File([file], name, { type: file.type }), rel: name }
      }))
    }
    document.addEventListener('paste', onPaste)
    return () => document.removeEventListener('paste', onPaste)
  }, [tab, ws.off, queueFiles])

  // Files dragged from outside anywhere over the page show the drop target;
  // a drop anywhere uploads to the open folder, or to the folder row (or
  // crumb) under the pointer. A drop never makes the browser open the file.
  useEffect(() => {
    if (tab !== 'files' || ws.off) return undefined
    const enter = (e) => {
      if (!hasFiles(e.dataTransfer)) return
      dragDepth.current++
      setDropActive(true)
    }
    const over = (e) => { if (hasFiles(e.dataTransfer)) e.preventDefault() }
    const leave = (e) => {
      if (!hasFiles(e.dataTransfer)) return
      dragDepth.current = Math.max(0, dragDepth.current - 1)
      if (dragDepth.current === 0) { setDropActive(false); setDropPath(null) }
    }
    const drop = (e) => {
      if (!hasFiles(e.dataTransfer)) return
      e.preventDefault()
      dragDepth.current = 0
      setDropActive(false)
      setDropPath(null)
      filesOfDrop(e.dataTransfer).then(list => queueFiles(list))
    }
    window.addEventListener('dragenter', enter)
    window.addEventListener('dragover', over)
    window.addEventListener('dragleave', leave)
    window.addEventListener('drop', drop)
    return () => {
      window.removeEventListener('dragenter', enter)
      window.removeEventListener('dragover', over)
      window.removeEventListener('dragleave', leave)
      window.removeEventListener('drop', drop)
    }
  }, [tab, ws.off, queueFiles])

  // ---- actions -----------------------------------------------------------------

  const undoToast = (message, revs, done) => addToast(message, 'success', undefined, {
    action: revs?.length ? {
      label: t('toast.undo'),
      onClick: async () => { if (await ws.undoRevs(revs)) addToast(done, 'info') },
    } : undefined,
    testId: 'ws-toast',
  })

  const doDelete = async (list) => {
    const revs = await ws.removeMany(list)
    if (!revs) return
    setSelected(new Set())
    if (list.some(e => e.path === previewPath)) setPreviewPath(null)
    undoToast(list.length === 1 ? t('toast.deletedOne', { name: list[0].name }) : t('toast.deleted', { count: list.length }), revs, t('toast.restored'))
  }
  const remove = (list) => {
    const guards = list.map(guardOf).filter(Boolean)
    if (guards.length) setGuarded({ entries: list, action: 'delete', guard: guards[0] })
    else doDelete(list)
  }
  const doRename = async (entry, name) => {
    const node = await ws.rename(entry, name)
    if (!node) return
    if (previewPath === entry.path) setPreviewPath(node.path)
    setCursor(node.path)
    undoToast(t('toast.renamed', { name }), node.rev ? [node.rev] : [], t('toast.renameUndone'))
  }
  const rename = (entry, name) => {
    setRenaming(null)
    const guard = guardOf(entry)
    if (guard) setGuarded({ entries: [entry], action: 'rename', name, guard })
    else doRename(entry, name)
  }
  const confirmGuarded = async () => {
    const g = guarded
    setGuarded(null)
    if (!g) return
    if (g.action === 'delete') await doDelete(g.entries)
    else await doRename(g.entries[0], g.name)
  }
  const moveTo = async (list, dest) => {
    setMoving(null)
    const items = list.filter(e => parentOf(e.path) !== dest && e.path !== dest && !dest.startsWith(`${e.path}/`))
    if (!items.length) return
    const revs = await ws.move(items, dest)
    if (!revs) return
    setSelected(new Set())
    const folder = dest === '/' ? t('home') : dest.split('/').pop()
    undoToast(items.length === 1 ? t('toast.movedOne', { name: items[0].name, folder }) : t('toast.moved', { count: items.length, folder }), revs, t('toast.moveUndone'))
  }
  const create = async (name) => {
    const kind = creating?.kind
    setCreating(null)
    if (kind === 'dir') {
      const node = await ws.mkdir(name)
      if (node?.path) setCursor(node.path)
    } else {
      const node = await ws.touch(name)
      if (node?.path) { setCursor(node.path); setPreviewPath(node.path) }
    }
  }
  const startCreate = (kind) => {
    setTab('files')
    setQuery('')
    setRenaming(null)
    setCreating({ kind, name: uniqueName(kind === 'dir' ? t('newFolderName') : t('newFileName'), taken) })
  }
  const download = (list) => {
    const files = list.filter(e => e.kind === 'file')
    files.forEach((e, i) => setTimeout(() => {
      const a = document.createElement('a')
      a.href = filesApi.contentUrl(e.path)
      a.download = e.name
      a.click()
    }, i * 250))
  }
  const openEntry = (entry) => {
    if (entry.kind === 'dir') openFolder(entry.path)
    else { setPreviewPath(entry.path); setCursor(entry.path) }
  }

  const menuItems = (entry) => {
    const isDir = entry.kind === 'dir'
    const many = selected.has(entry.path) && selected.size > 1
    const list = many ? picked : [entry]
    return [
      { key: 'select', icon: 'check', label: t('select.start'), onClick: () => ctx.onToggle(entry), hidden: !touch || selected.has(entry.path) },
      { key: 'open', icon: isDir ? 'folder' : 'eye', label: isDir ? t('menu.openFolder') : t('menu.preview'), onClick: () => openEntry(entry), hidden: many },
      { key: 'download', icon: 'download', label: many ? t('menu.downloadMany', { count: list.filter(e => e.kind === 'file').length }) : t('download'), onClick: () => download(list), hidden: !list.some(e => e.kind === 'file') },
      { key: 'rename', icon: 'pencil', label: t('rename'), shortcut: 'F2', onClick: () => { setCreating(null); afterMenu(() => setRenaming(entry.path)) }, hidden: many },
      { key: 'move', icon: 'arrow-right', label: many ? t('menu.moveMany', { count: list.length }) : t('moveTo'), onClick: () => setMoving(list) },
      { divider: true },
      { key: 'delete', icon: 'trash', label: many ? t('menu.deleteMany', { count: list.length }) : t('delete'), shortcut: t('keys.delete'), danger: true, onClick: () => remove(list) },
    ]
  }

  // ---- selection ---------------------------------------------------------------

  const paths = order.map(e => e.path)
  const selectOnly = (p) => { setSelected(new Set([p])); setAnchor(p); setCursor(p) }
  const ctx = {
    t, locale: i18n.language, now, taken, touch, menuItems,
    onOpen: openEntry,
    onRowClick: (entry, e) => {
      if (e.target.closest('button, a, input')) return
      setCursor(entry.path)
      if (e.shiftKey) {
        const range = rangeOf(paths, anchor, entry.path)
        setSelected(prev => new Set(e.metaKey || e.ctrlKey ? [...prev, ...range] : range))
      } else if (e.metaKey || e.ctrlKey || (touch && selected.size > 0)) {
        setSelected(prev => {
          const next = new Set(prev)
          if (next.has(entry.path)) next.delete(entry.path)
          else next.add(entry.path)
          return next
        })
        setAnchor(entry.path)
      } else if (touch) openEntry(entry)
      else selectOnly(entry.path)
    },
    onToggle: (entry, shift) => {
      if (shift && anchor) {
        const range = rangeOf(paths, anchor, entry.path)
        setSelected(prev => new Set([...prev, ...range]))
        return
      }
      setSelected(prev => {
        const next = new Set(prev)
        if (next.has(entry.path)) next.delete(entry.path)
        else next.add(entry.path)
        return next
      })
      setAnchor(entry.path)
      setCursor(entry.path)
    },
    onExtend: (p) => {
      const from = anchor || cursor || p
      if (!anchor) setAnchor(from)
      setSelected(new Set(rangeOf(paths, from, p)))
    },
    onSelectAll: () => setSelected(new Set(paths)),
    onClear: () => setSelected(new Set()),
    onFocusRow: (entry) => setCursor(entry.path),
    onStartRename: (entry) => { setCreating(null); setRenaming(entry.path) },
    onRename: rename,
    onRenameCancel: () => setRenaming(null),
    onCreate: create,
    onCreateCancel: () => setCreating(null),
    onDeleteKey: (entry) => remove(selected.size > 0 ? picked : [entry]),
    onUp: () => { if (ws.path !== '/') openFolder(parentOf(ws.path)) },
    onContextMenu: (entry, e) => {
      if (!selected.has(entry.path)) selectOnly(entry.path)
      setMenu({ entry, at: { x: e.clientX, y: e.clientY, returnFocus: e.currentTarget } })
    },
    // Rows dragged onto a folder move there.
    onDragStart: (entry, e) => {
      const list = selected.has(entry.path) ? picked : [entry]
      dragged.current = list
      setDragCount(list.length)
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData(DRAG_TYPE, JSON.stringify(list.map(x => x.path)))
      e.dataTransfer.setData('text/plain', list.map(x => x.name).join('\n'))
      if (chipRef.current) e.dataTransfer.setDragImage(chipRef.current, 12, 12)
    },
    onDragEnd: () => { dragged.current = null; setDropPath(null) },
    onFolderDragOver: (entry, e) => dropTarget.over(entry.path, e),
    onFolderDragLeave: (entry) => dropTarget.leave(entry.path),
    onFolderDrop: (entry, e) => dropTarget.drop(entry.path, e),
  }

  // A drop target for a folder path: rows dragged in the page move there,
  // files from outside upload there.
  const dropTarget = {
    path: dropPath,
    over: (to, e) => {
      const types = Array.from(e.dataTransfer?.types || [])
      const own = types.includes(DRAG_TYPE)
      if (own && (dragged.current || []).some(x => x.path === to || parentOf(x.path) === to || to.startsWith(`${x.path}/`))) return
      if (!own && !hasFiles(e.dataTransfer)) return
      e.preventDefault()
      e.dataTransfer.dropEffect = own ? 'move' : 'copy'
      if (dropPath !== to) setDropPath(to)
    },
    leave: (to) => { if (dropPath === to) setDropPath(null) },
    drop: (to, e) => {
      e.preventDefault()
      e.stopPropagation()
      setDropPath(null)
      dragDepth.current = 0
      setDropActive(false)
      const raw = e.dataTransfer.getData(DRAG_TYPE)
      if (raw) {
        const list = (dragged.current || []).length ? dragged.current : JSON.parse(raw).map(p => byPath.get(p)).filter(Boolean)
        dragged.current = null
        moveTo(list, to)
      } else if (hasFiles(e.dataTransfer)) {
        filesOfDrop(e.dataTransfer).then(list => queueFiles(list, to))
      }
    },
  }

  const setSort = (key) => setPrefs(prefs.key === key ? { dir: prefs.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: key === 'name' || key === 'type' ? 'asc' : 'desc' })

  // ---- page --------------------------------------------------------------------

  if (ws.off) {
    return (
      <div className="ws-page" data-testid="workspace-page">
        <div className="ws-main">
          <h1 className="ws-title">{t('title')}</h1>
          <Callout tone="info" testId="files-off">{ws.off}</Callout>
        </div>
      </div>
    )
  }

  const folderName = ws.path === '/' ? t('home') : ws.path.split('/').pop()
  const firstLoad = !ws.loaded && !ws.loadError
  const empty = ws.loaded && ws.entries.length === 0 && !creating
  const noMatch = ws.loaded && query && filtered.length === 0
  const searching = ws.hits !== null

  return (
    <div className={`ws-page${preview ? ' ws-page--preview' : ''}`} data-testid="workspace-page">
      <div className="ws-main">
        <header className="ws-head">
          <h1 className="ws-title">{t('title')}</h1>
          <div className="ws-head__actions">
            <Button size="sm" variant="ghost" icon="plus" className="ws-act" aria-label={t('newFile')} title={t('newFile')} onClick={() => startCreate('file')}>
              {t('newFile')}
            </Button>
            <Button size="sm" icon="folder" className="ws-act" aria-label={t('newFolder')} title={t('newFolder')} onClick={() => startCreate('dir')}>
              {t('newFolder')}
            </Button>
            <span className="ws-split">
              <Button size="sm" variant="primary" icon="upload" onClick={() => fileInput.current?.click()} data-testid="ws-upload">
                {t('upload')}
              </Button>
              <ActionMenu
              compact icon="chevron-down" triggerClassName="ws-split__more" ariaLabel={t('uploadMenu')} triggerLabel={t('uploadMenu')}
              items={[
                { key: 'files', icon: 'file', label: t('uploadFiles'), onClick: () => fileInput.current?.click() },
                { key: 'folder', icon: 'folder', label: t('uploadFolder'), onClick: () => folderInput.current?.click() },
              ]}
              />
            </span>
            <input ref={fileInput} type="file" multiple hidden data-testid="ws-file-input" aria-label={t('upload')} onChange={pickFiles} />
            <input ref={folderInput} type="file" multiple hidden webkitdirectory="" data-testid="ws-folder-input" aria-label={t('uploadFolder')} onChange={pickFiles} />
          </div>
        </header>

        <div className="ws-tabs" role="tablist" aria-label={t('tabs.label')}>
          {['files', 'trash', 'history', 'shared'].map((k) => (
            <button
              key={k} type="button" role="tab" aria-selected={tab === k} id={`ws-tab-${k}`}
              className={`ws-tab${tab === k ? ' ws-tab--on' : ''}`} onClick={() => setTab(k)}
            >
              {t(`tabs.${k}`)}
              {k === 'trash' && ws.trash.length > 0 && <span className="ws-tab__count" aria-label={t('tabs.trashCount', { count: ws.trash.length })}>{ws.trash.length}</span>}
            </button>
          ))}
        </div>

        {ws.error && (
          <Callout
            tone="error" role="alert" className="ws-alert"
            actions={<Button size="sm" variant="ghost" onClick={ws.clearError}>{t('dismiss')}</Button>}
          >
            {ws.error}
          </Callout>
        )}

        {tab === 'files' && (
          <section className="ws-files" aria-labelledby="ws-tab-files" data-drop={dropActive || undefined}>
            <div className="ws-bar">
              {picked.length > 0 ? (
                <div className="ws-selbar" role="toolbar" aria-label={t('select.bar')} data-testid="ws-selection-bar">
                  <Button size="sm" variant="ghost" icon="close" aria-label={t('select.clear')} title={t('select.clear')} onClick={() => setSelected(new Set())} />
                  <span className="ws-selbar__count" aria-live="polite">{t('select.count', { count: picked.length })}</span>
                  <span className="ws-selbar__acts">
                    {picked.some(e => e.kind === 'file') && (
                      <Button size="sm" variant="ghost" icon="download" aria-label={t('download')} title={t('download')} onClick={() => download(picked)}>{t('download')}</Button>
                    )}
                    <Button size="sm" variant="ghost" icon="arrow-right" aria-label={t('moveTo')} title={t('moveTo')} onClick={() => setMoving(picked)}>{t('moveTo')}</Button>
                    <Button size="sm" variant="danger-ghost" icon="trash" aria-label={t('delete')} title={t('delete')} onClick={() => remove(picked)}>{t('delete')}</Button>
                  </span>
                </div>
              ) : (
                <>
                  <Breadcrumb path={ws.path} onOpen={openFolder} drop={dropTarget} t={t} />
                  <form className="ws-search" role="search" onSubmit={(e) => { e.preventDefault(); if (query.trim()) { setSearched(query.trim()); ws.search(query) } }}>
                    <Icon name="search" className="ws-search__icon" />
                    <input
                      ref={searchRef} className="input ws-search__input" type="search" aria-label={t('search.label')} placeholder={t('search.placeholder')}
                      value={query}
                      onChange={(e) => { setQuery(e.target.value); if (!e.target.value) { ws.clearSearch(); setSearched('') } }}
                      onKeyDown={(e) => { if (e.key === 'Escape' && query) { e.preventDefault(); setQuery(''); ws.clearSearch(); setSearched('') } }}
                    />
                  </form>
                  <div className="ws-views" role="radiogroup" aria-label={t('view.label')}>
                    {['list', 'grid'].map(v => (
                      <button
                        key={v} type="button" role="radio" aria-checked={prefs.view === v} className={`ws-view${prefs.view === v ? ' ws-view--on' : ''}`}
                        aria-label={t(`view.${v}`)} title={t(`view.${v}`)} onClick={() => setPrefs({ view: v })} data-testid={`ws-view-${v}`}
                      >
                        <Icon name={v === 'list' ? 'list' : 'images'} />
                      </button>
                    ))}
                  </div>
                </>
              )}
            </div>

            {query && !searching && filtered.length > 0 && (
              <p className="ws-filtering" data-testid="ws-filtering">
                <span>{t('search.filtering', { count: filtered.length, query })}</span>
                <button type="button" className="ws-link" onClick={() => { setSearched(query.trim()); ws.search(query) }}>
                  {t('search.everywhere', { query: query.trim() })}
                </button>
              </p>
            )}

            {isSettingsFolder(ws.path) && (
              <p className="ws-folder-about" data-testid="ws-folder-about">
                <span className="ws-managed-tag">{t('managed.marker')}</span> {t('managed.about.localai')}
              </p>
            )}

            {searching ? (
              <SearchHits
                hits={ws.hits} query={searched} t={t}
                onClear={() => { ws.clearSearch(); setSearched('') }}
                onOpenHit={(h) => {
                  ws.clearSearch()
                  setSearched('')
                  setQuery('')
                  openFolder(parentOf(h.path))
                  setPreviewPath(h.path)
                }}
              />
            ) : ws.loadError ? (
              <LoadError title={t('loadFailed', { folder: folderName })} error={ws.loadError} onRetry={() => ws.refresh()} testId="ws-load-error" />
            ) : firstLoad ? (
              <SkeletonRows count={6} label={t('loading')} testId="ws-skeleton" className="ws-skeleton" />
            ) : empty ? (
              <EmptyState
                icon="cloud-upload" testId="ws-empty" className="ws-empty-state"
                title={ws.path === '/' ? t('emptyHome.title') : t('emptyFolder.title')}
                body={touch ? t('emptyFolder.bodyTouch') : t('emptyFolder.body')}
                actions={(
                  <>
                    <Button variant="primary" icon="upload" onClick={() => fileInput.current?.click()}>{t('upload')}</Button>
                    <Button variant="ghost" icon="chat" to="/app">{t('emptyFolder.ask')}</Button>
                  </>
                )}
              />
            ) : noMatch ? (
              <EmptyState
                variant="no-results" testId="ws-no-match" title={t('search.noMatch', { query })}
                actions={(
                  <>
                    <Button size="sm" icon="search" onClick={() => { setSearched(query.trim()); ws.search(query) }}>{t('search.everywhereShort')}</Button>
                    <Button size="sm" variant="ghost" onClick={() => setQuery('')}>{t('search.clear')}</Button>
                  </>
                )}
              />
            ) : (
              <FileList
                groups={groups} view={prefs.view} sort={prefs} onSort={setSort} selected={selected}
                cursor={cursor} setCursor={setCursor} previewPath={previewPath} renaming={renaming} creating={creating}
                dropPath={dropPath} ctx={ctx} t={t}
              />
            )}

            {dropActive && (
              <div className="ws-drop" aria-hidden="true" data-testid="ws-drop-overlay">
                <div className="ws-drop__card">
                  <Icon name="cloud-upload" className="ws-drop__icon" />
                  <p className="ws-drop__title">{t('drop.title', { folder: dropPath ? dropPath.split('/').pop() || t('home') : folderName })}</p>
                  <p className="ws-drop__body">{ws.maxFileSize > 0 ? t('drop.limit', { size: fileSize(ws.maxFileSize) }) : t('drop.body')}</p>
                </div>
              </div>
            )}
          </section>
        )}
        {tab === 'trash' && <TrashList ws={ws} t={t} />}
        {tab === 'history' && <HistoryList ws={ws} t={t} />}
        {tab === 'shared' && <SharedFolders />}
      </div>

      {preview && tab === 'files' && !searching && (
        <FilePreview
          entry={preview} onClose={() => setPreviewPath(null)}
          onRename={(e) => { setPreviewPath(null); setRenaming(e.path) }}
          onMove={(e) => setMoving([e])}
          onDelete={(e) => remove([e])}
        />
      )}

      <UploadTray state={upState} queue={uploads} onOpen={(item) => {
        const dir = item.result?.path ? parentOf(item.result.path) : item.dir
        if (dir !== ws.path) openFolder(dir)
        setPreviewPath(item.result?.path || null)
      }} />

      {menu && (
        <ActionMenu
          key={`${menu.at.x}:${menu.at.y}`} at={menu.at} onClose={() => setMenu(null)}
          items={menuItems(menu.entry)} ariaLabel={t('menu.label', { name: menu.entry.name })}
        />
      )}
      {moving && <MoveDialog items={moving} start={ws.path} onMove={(dest) => moveTo(moving, dest)} onCancel={() => setMoving(null)} />}
      {conflict && <ConflictDialog names={conflict.names} folder={conflict.folder} onChoose={conflict.resolve} onCancel={() => conflict.resolve(null)} />}
      <div className="ws-dragchip" ref={chipRef} aria-hidden="true">
        <Icon name="file" /> {t('select.count', { count: dragCount || 1 })}
      </div>
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
