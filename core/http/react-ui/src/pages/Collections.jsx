import { useState, useEffect, useCallback } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentCollectionsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import UserGroupSection from '../components/UserGroupSection'
import ConfirmDialog from '../components/ConfirmDialog'

export default function Collections() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t } = useTranslation('collections')
  const { isAdmin, authEnabled, user } = useAuth()
  const userMap = useUserMap()
  const [collections, setCollections] = useState([])
  const [loading, setLoading] = useState(true)
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)
  const [userGroups, setUserGroups] = useState(null)
  const [confirmDialog, setConfirmDialog] = useState(null)

  const fetchCollections = useCallback(async () => {
    try {
      const data = await agentCollectionsApi.list(isAdmin && authEnabled)
      setCollections(Array.isArray(data.collections) ? data.collections : [])
      setUserGroups(data.user_groups || null)
    } catch (err) {
      addToast(t('toasts.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast, isAdmin, authEnabled, t])

  useEffect(() => {
    fetchCollections()
  }, [fetchCollections])

  const handleCreate = async () => {
    const name = newName.trim()
    if (!name) return
    setCreating(true)
    try {
      await agentCollectionsApi.create(name)
      addToast(t('toasts.created', { name }), 'success')
      setNewName('')
      fetchCollections()
    } catch (err) {
      addToast(t('toasts.createFailed', { message: err.message }), 'error')
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = (name, userId) => {
    setConfirmDialog({
      title: t('deleteDialog.title'),
      message: t('deleteDialog.message', { name }),
      confirmLabel: t('deleteDialog.confirm'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await agentCollectionsApi.reset(name, userId)
          addToast(t('toasts.deleted', { name }), 'success')
          fetchCollections()
        } catch (err) {
          addToast(t('toasts.deleteFailed', { message: err.message }), 'error')
        }
      },
    })
  }

  const handleReset = (name, userId) => {
    setConfirmDialog({
      title: t('resetDialog.title'),
      message: t('resetDialog.message', { name }),
      confirmLabel: t('resetDialog.confirm'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await agentCollectionsApi.reset(name, userId)
          addToast(t('toasts.reset', { name }), 'success')
          fetchCollections()
        } catch (err) {
          addToast(t('toasts.resetFailed', { message: err.message }), 'error')
        }
      },
    })
  }

  return (
    <div className="page page--wide">
      <style>{`
        .collections-create-bar {
          display: flex;
          gap: var(--spacing-sm);
          margin-bottom: var(--spacing-lg);
        }
        .collections-create-bar .input {
          flex: 1;
        }
        .collections-grid {
          display: grid;
          grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
          gap: var(--spacing-md);
        }
        .collections-card-name {
          font-size: 1rem;
          font-weight: 600;
          margin-bottom: var(--spacing-sm);
          word-break: break-word;
        }
        .collections-card-actions {
          display: flex;
          gap: var(--spacing-xs);
          margin-top: var(--spacing-md);
        }
      `}</style>

      <div className="page-header">
        <h1 className="page-title">{t('title')}</h1>
        <p className="page-subtitle">{t('subtitle')}</p>
      </div>

      <div className="collections-create-bar">
        <input
          className="input"
          type="text"
          placeholder={t('newPlaceholder')}
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') handleCreate() }}
        />
        <button className="btn btn-primary" onClick={handleCreate} disabled={creating || !newName.trim()}>
          {creating ? <><i className="fas fa-spinner fa-spin" /> {t('actions.creating')}</> : <><i className="fas fa-plus" /> {t('actions.create')}</>}
        </button>
      </div>

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <i className="fas fa-spinner fa-spin" style={{ fontSize: '2rem', color: 'var(--color-text-muted)' }} />
        </div>
      ) : collections.length === 0 && !userGroups ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-database" /></div>
          <h2 className="empty-state-title">{t('empty.title')}</h2>
          <p className="empty-state-text">
            {t('empty.text')}
          </p>
        </div>
      ) : (
        <>
        {userGroups && <h2 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>{t('sections.yourCollections')}</h2>}
        {collections.length === 0 ? (
          <p style={{ color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)' }}>{t('empty.noPersonal')}</p>
        ) : (
        <div className="collections-grid">
          {collections.map((collection) => {
            const name = typeof collection === 'string' ? collection : collection.name
            return (
              <div className="card" key={name} style={{ cursor: 'pointer' }} onClick={() => navigate(`/app/collections/${encodeURIComponent(name)}`)}>
                <div className="collections-card-name">
                  <i className="fas fa-folder" style={{ marginRight: 'var(--spacing-xs)', color: 'var(--color-primary)' }} />
                  {name}
                </div>
                <div className="collections-card-actions" onClick={(e) => e.stopPropagation()}>
                  <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/collections/${encodeURIComponent(name)}`)} title={t('actions.viewDetails')}>
                    <i className="fas fa-eye" /> {t('actions.details')}
                  </button>
                  <button className="btn btn-secondary btn-sm" onClick={() => handleReset(name)} title={t('actions.resetCollection')}>
                    <i className="fas fa-rotate" /> {t('actions.reset')}
                  </button>
                  <button className="btn btn-danger btn-sm" onClick={() => handleDelete(name)} title={t('actions.deleteCollection')}>
                    <i className="fas fa-trash" />
                  </button>
                </div>
              </div>
            )
          })}
        </div>
        )}
        </>
      )}

      {userGroups && (
        <UserGroupSection
          title={t('sections.otherUsersCollections')}
          userGroups={userGroups}
          userMap={userMap}
          currentUserId={user?.id}
          itemKey="collections"
          renderGroup={(items, userId) => (
            <div className="collections-grid">
              {(items || []).map((col) => {
                const name = typeof col === 'string' ? col : col.name
                return (
                  <div className="card" key={name}>
                    <div className="collections-card-name">
                      <i className="fas fa-folder" style={{ marginRight: 'var(--spacing-xs)', color: 'var(--color-primary)' }} />
                      {name}
                    </div>
                    <div className="collections-card-actions">
                      <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/collections/${encodeURIComponent(name)}?user_id=${encodeURIComponent(userId)}`)} title={t('actions.viewDetails')}>
                        <i className="fas fa-eye" /> {t('actions.details')}
                      </button>
                      <button className="btn btn-secondary btn-sm" onClick={() => handleReset(name, userId)} title={t('actions.resetCollection')}>
                        <i className="fas fa-rotate" /> {t('actions.reset')}
                      </button>
                      <button className="btn btn-danger btn-sm" onClick={() => handleDelete(name, userId)} title={t('actions.deleteCollection')}>
                        <i className="fas fa-trash" />
                      </button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        />
      )}

      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger={confirmDialog?.danger}
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => setConfirmDialog(null)}
      />
    </div>
  )
}
