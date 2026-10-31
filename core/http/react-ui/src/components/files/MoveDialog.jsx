/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import Button from '../ui/Button'
import Icon from '../Icon'
import { filesApi } from '../../utils/api'
import { parentOf } from '../../hooks/useWorkspace'
import { sortEntries } from '../../utils/fileBrowser'

// Picks the folder that the selection moves to. It opens on the folder of
// the selection and walks the tree one folder at a time; a folder that is
// moving, or is inside one, cannot be the target.
//   items   the entries that move
//   start   the folder to open on
//   onMove(dest)  onCancel()
export default function MoveDialog({ items, start, onMove, onCancel }) {
  const { t } = useTranslation('workspace')
  const [at, setAt] = useState(start || '/')
  const [state, setState] = useState({ status: 'loading', folders: [] })
  const moving = new Set(items.map(e => e.path))
  const blocked = (p) => [...moving].some(m => p === m || p.startsWith(`${m}/`))
  const here = items.every(e => parentOf(e.path) === at)

  useEffect(() => {
    let current = true
    setState(s => ({ ...s, status: 'loading' }))
    filesApi.list(at)
      .then(res => { if (current) setState({ status: 'ready', folders: sortEntries((res.entries || []).filter(e => e.kind === 'dir')) }) })
      .catch(error => { if (current) setState({ status: 'error', folders: [], error }) })
    return () => { current = false }
  }, [at])

  const parts = at.split('/').filter(Boolean)
  return (
    <Dialog
      title={t('move.title', { count: items.length })}
      description={items.length === 1 ? items[0].name : t('move.items', { count: items.length })}
      onClose={onCancel}
      closeLabel={t('move.cancel')}
      labelId="ws-move-title"
      testId="ws-move"
      foot={(
        <>
          <Button variant="ghost" onClick={onCancel}>{t('move.cancel')}</Button>
          <Button variant="primary" icon="arrow-right" disabled={here || blocked(at)} onClick={() => onMove(at)} data-testid="ws-move-here">
            {t('move.here', { folder: parts.length ? parts[parts.length - 1] : t('home') })}
          </Button>
        </>
      )}
    >
      <nav className="ws-move__path" aria-label={t('breadcrumb')}>
        <button type="button" className="ws-move__crumb" onClick={() => setAt('/')}><Icon name="home" />{t('home')}</button>
        {parts.map((p, i) => {
          const to = `/${parts.slice(0, i + 1).join('/')}`
          return (
            <span key={to} className="ws-move__step">
              <Icon name="chevron-right" className="ws-move__sep" />
              <button type="button" className="ws-move__crumb" aria-current={to === at ? 'location' : undefined} onClick={() => setAt(to)}>{p}</button>
            </span>
          )
        })}
      </nav>
      <ul className="ws-move__list" aria-busy={state.status === 'loading'}>
        {state.status === 'error' && <li className="ws-move__note">{t('move.failed')}</li>}
        {state.status === 'ready' && state.folders.length === 0 && <li className="ws-move__note">{t('move.noFolders')}</li>}
        {state.folders.map(f => (
          <li key={f.path}>
            <button type="button" className="ws-move__folder" disabled={blocked(f.path)} onClick={() => setAt(f.path)}>
              <Icon name="folder" />
              <span className="ws-move__name">{f.name}</span>
              <Icon name="chevron-right" className="ws-move__go" />
            </button>
          </li>
        ))}
      </ul>
    </Dialog>
  )
}
