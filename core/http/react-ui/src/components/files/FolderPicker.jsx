import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { filesApi } from '../../utils/api'
import { sortEntries } from '../../utils/fileBrowser'
import './folderPicker.css'

// Walks the folders of the person's files one folder at a time: a
// breadcrumb, then the folders of the open one. The open folder is the
// choice; the dialog around it says what choosing does. A folder that cannot
// be chosen is shown disabled, with the reason as a hint when there is one.
//   at          the open folder ("/" for all the files)
//   onAt(path)  opens another folder
//   blocked(entry)  true for a folder that cannot be opened or chosen
//   hintOf(entry)   the reason shown on a blocked folder, or ''
//   emptyText   the line of a folder without folders
export default function FolderPicker({ at, onAt, blocked = () => false, hintOf = () => '', emptyText }) {
  const { t } = useTranslation('workspace')
  const [state, setState] = useState({ status: 'loading', folders: [] })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let current = true
    setState(s => ({ ...s, status: 'loading' }))
    filesApi.list(at)
      .then(res => { if (current) setState({ status: 'ready', folders: sortEntries((res.entries || []).filter(e => e.kind === 'dir')) }) })
      .catch(error => { if (current) setState({ status: 'error', folders: [], error }) })
    return () => { current = false }
  }, [at, attempt])

  const parts = at.split('/').filter(Boolean)
  return (
    <div className="ws-move__picker" data-testid="folder-picker">
      <nav className="ws-move__path" aria-label={t('breadcrumb')}>
        <button type="button" className="ws-move__crumb" aria-current={at === '/' ? 'location' : undefined} onClick={() => onAt('/')}><Icon name="home" />{t('home')}</button>
        {parts.map((p, i) => {
          const to = `/${parts.slice(0, i + 1).join('/')}`
          return (
            <span key={to} className="ws-move__step">
              <Icon name="chevron-right" className="ws-move__sep" />
              <button type="button" className="ws-move__crumb" aria-current={to === at ? 'location' : undefined} onClick={() => onAt(to)}>{p}</button>
            </span>
          )
        })}
      </nav>
      <ul className="ws-move__list" aria-busy={state.status === 'loading'}>
        {state.status === 'loading' && state.folders.length === 0 && <li className="ws-move__note">{t('loading')}</li>}
        {state.status === 'error' && (
          <li className="ws-move__note" role="alert">
            {t('move.failed')}
            <button type="button" className="ws-move__crumb ws-move__retry" onClick={() => setAttempt(n => n + 1)}>{t('picker.retry')}</button>
          </li>
        )}
        {state.status === 'ready' && state.folders.length === 0 && <li className="ws-move__note">{emptyText || t('picker.noFolders')}</li>}
        {state.folders.map(f => {
          const off = blocked(f)
          const hint = off ? hintOf(f) : ''
          return (
            <li key={f.path}>
              <button type="button" className="ws-move__folder" disabled={off} onClick={() => onAt(f.path)} title={hint || undefined}>
                <Icon name="folder" />
                <span className="ws-move__name">{f.name}</span>
                {hint ? <span className="ws-move__hint">{hint}</span> : <Icon name="chevron-right" className="ws-move__go" />}
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
