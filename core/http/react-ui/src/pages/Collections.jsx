import { useState, useEffect, useCallback } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { agentCollectionsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import UserGroupSection from '../components/UserGroupSection'

export default function Collections() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { isAdmin, authEnabled, user } = useAuth()
  const userMap = useUserMap()
  const [collections, setCollections] = useState([])
  const [loading, setLoading] = useState(true)
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)
  const [userGroups, setUserGroups] = useState(null)

  const fetchCollections = useCallback(async () => {
    try {
      const data = await agentCollectionsApi.list(isAdmin && authEnabled)
      setCollections(Array.isArray(data.collections) ? data.collections : [])
      setUserGroups(data.user_groups || null)
    } catch (err) {
      addToast(`Failed to load collections: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast, isAdmin, authEnabled])

  useEffect(() => {
    fetchCollections()
  }, [fetchCollections])

  const handleCreate = async () => {
    const name = newName.trim()
    if (!name) return
    setCreating(true)
    try {
      await agentCollectionsApi.create(name)
      addToast(`Collection "${name}" created`, 'success')
      setNewName('')
      fetchCollections()
    } catch (err) {
      addToast(`Failed to create collection: ${err.message}`, 'error')
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async (name, userId) => {
    if (!window.confirm(`Delete collection "${name}"? This will remove all entries and cannot be undone.`)) return
    try {
      await agentCollectionsApi.reset(name, userId)
      addToast(`Collection "${name}" deleted`, 'success')
      fetchCollections()
    } catch (err) {
      addToast(`Failed to delete collection: ${err.message}`, 'error')
    }
  }

  const handleReset = async (name, userId) => {
    if (!window.confirm(`Reset collection "${name}"? This will remove all entries but keep the collection.`)) return
    try {
      await agentCollectionsApi.reset(name, userId)
      addToast(`Collection "${name}" reset`, 'success')
      fetchCollections()
    } catch (err) {
      addToast(`Failed to reset collection: ${err.message}`, 'error')
    }
  }

  return (
    <div className="page">
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
        <h1 className="page-title">Knowledge Base</h1>
        <p className="page-subtitle">Manage document collections for agent RAG</p>
      </div>

      <div className="collections-create-bar">
        <input
          className="input"
          type="text"
          placeholder="New collection name..."
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') handleCreate() }}
        />
        <button className="btn btn-primary" onClick={handleCreate} disabled={creating || !newName.trim()}>
          {creating ? <><i className="fas fa-spinner fa-spin" /> Creating...</> : <><i className="fas fa-plus" /> Create</>}
        </button>
      </div>

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <i className="fas fa-spinner fa-spin" style={{ fontSize: '2rem', color: 'var(--color-text-muted)' }} />
        </div>
      ) : collections.length === 0 && !userGroups ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-database" /></div>
          <h2 className="empty-state-title">No collections yet</h2>
          <p className="empty-state-text">Create a collection above to start building your knowledge base.</p>
        </div>
      ) : (
        <>
        {userGroups && <h2 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>Your Collections</h2>}
        {collections.length === 0 ? (
          <p style={{ color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)' }}>You have no collections yet.</p>
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
                  <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/collections/${encodeURIComponent(name)}`)} title="View details">
                    <i className="fas fa-eye" /> Details
                  </button>
                  <button className="btn btn-secondary btn-sm" onClick={() => handleReset(name)} title="Reset collection">
                    <i className="fas fa-rotate" /> Reset
                  </button>
                  <button className="btn btn-danger btn-sm" onClick={() => handleDelete(name)} title="Delete collection">
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
          title="Other Users' Collections"
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
                      <button className="btn btn-secondary btn-sm" onClick={() => navigate(`/app/collections/${encodeURIComponent(name)}?user_id=${encodeURIComponent(userId)}`)} title="View details">
                        <i className="fas fa-eye" /> Details
                      </button>
                      <button className="btn btn-secondary btn-sm" onClick={() => handleReset(name, userId)} title="Reset collection">
                        <i className="fas fa-rotate" /> Reset
                      </button>
                      <button className="btn btn-danger btn-sm" onClick={() => handleDelete(name, userId)} title="Delete collection">
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
    </div>
  )
}
