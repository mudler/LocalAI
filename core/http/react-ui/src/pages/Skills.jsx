import { useState, useEffect, useCallback } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { skillsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import UserGroupSection from '../components/UserGroupSection'

export default function Skills() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { isAdmin, authEnabled, user } = useAuth()
  const userMap = useUserMap()
  const [skills, setSkills] = useState([])
  const [searchQuery, setSearchQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [importing, setImporting] = useState(false)
  const [unavailable, setUnavailable] = useState(false)
  const [showGitRepos, setShowGitRepos] = useState(false)
  const [gitRepos, setGitRepos] = useState([])
  const [gitRepoUrl, setGitRepoUrl] = useState('')
  const [gitReposLoading, setGitReposLoading] = useState(false)
  const [gitReposAction, setGitReposAction] = useState(null)
  const [userGroups, setUserGroups] = useState(null)

  const fetchSkills = useCallback(async () => {
    setLoading(true)
    setUnavailable(false)
    const timeoutMs = 15000
    const withTimeout = (p) =>
      Promise.race([
        p,
        new Promise((_, reject) =>
          setTimeout(() => reject(new Error('Request timed out')), timeoutMs)
        ),
      ])
    try {
      if (searchQuery.trim()) {
        const data = await withTimeout(skillsApi.search(searchQuery.trim()))
        setSkills(Array.isArray(data) ? data : [])
        setUserGroups(null)
      } else {
        const data = await withTimeout(skillsApi.list(isAdmin && authEnabled))
        // Handle wrapped response (admin) or flat array (regular user)
        if (Array.isArray(data)) {
          setSkills(data)
          setUserGroups(null)
        } else {
          setSkills(Array.isArray(data.skills) ? data.skills : [])
          setUserGroups(data.user_groups || null)
        }
      }
    } catch (err) {
      if (err.message?.includes('503') || err.message?.includes('skills')) {
        setUnavailable(true)
        setSkills([])
      } else {
        addToast(err.message || 'Failed to load skills', 'error')
        setSkills([])
      }
    } finally {
      setLoading(false)
    }
  }, [searchQuery, addToast, isAdmin, authEnabled])

  useEffect(() => {
    fetchSkills()
  }, [fetchSkills])

  const deleteSkill = async (name) => {
    if (!window.confirm(`Delete skill "${name}"? This action cannot be undone.`)) return
    try {
      await skillsApi.delete(name)
      addToast(`Skill "${name}" deleted`, 'success')
      fetchSkills()
    } catch (err) {
      addToast(err.message || 'Failed to delete skill', 'error')
    }
  }

  const exportSkill = async (name) => {
    try {
      const url = skillsApi.exportUrl(name)
      const res = await fetch(url, { credentials: 'same-origin' })
      if (!res.ok) throw new Error(res.statusText || 'Export failed')
      const blob = await res.blob()
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = `${name.replace(/\//g, '-')}.tar.gz`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(a.href)
      addToast(`Skill "${name}" exported`, 'success')
    } catch (err) {
      addToast(err.message || 'Export failed', 'error')
    }
  }

  const handleImport = async (e) => {
    const file = e.target.files?.[0]
    if (!file) return
    setImporting(true)
    try {
      await skillsApi.import(file)
      addToast(`Skill imported from "${file.name}"`, 'success')
      fetchSkills()
    } catch (err) {
      addToast(err.message || 'Import failed', 'error')
    } finally {
      setImporting(false)
      e.target.value = ''
    }
  }

  const loadGitRepos = async () => {
    setGitReposLoading(true)
    try {
      const list = await skillsApi.listGitRepos()
      setGitRepos(Array.isArray(list) ? list : [])
    } catch (err) {
      addToast(err.message || 'Failed to load Git repos', 'error')
      setGitRepos([])
    } finally {
      setGitReposLoading(false)
    }
  }

  useEffect(() => {
    if (showGitRepos) loadGitRepos()
  }, [showGitRepos])

  const addGitRepo = async (e) => {
    e.preventDefault()
    const url = gitRepoUrl.trim()
    if (!url) return
    setGitReposAction('add')
    try {
      await skillsApi.addGitRepo(url)
      setGitRepoUrl('')
      await loadGitRepos()
      fetchSkills()
      addToast('Git repo added and syncing', 'success')
    } catch (err) {
      addToast(err.message || 'Failed to add repo', 'error')
    } finally {
      setGitReposAction(null)
    }
  }

  const syncGitRepo = async (id) => {
    setGitReposAction(id)
    try {
      await skillsApi.syncGitRepo(id)
      await loadGitRepos()
      fetchSkills()
      addToast('Repo synced', 'success')
    } catch (err) {
      addToast(err.message || 'Sync failed', 'error')
    } finally {
      setGitReposAction(null)
    }
  }

  const toggleGitRepo = async (id) => {
    try {
      await skillsApi.toggleGitRepo(id)
      await loadGitRepos()
      fetchSkills()
      addToast('Repo toggled', 'success')
    } catch (err) {
      addToast(err.message || 'Toggle failed', 'error')
    }
  }

  const deleteGitRepo = async (id) => {
    if (!window.confirm('Remove this Git repository? Skills from it will no longer be available.')) return
    try {
      await skillsApi.deleteGitRepo(id)
      await loadGitRepos()
      fetchSkills()
      addToast('Repo removed', 'success')
    } catch (err) {
      addToast(err.message || 'Remove failed', 'error')
    }
  }

  if (unavailable) {
    return (
      <div className="page">
        <div className="page-header">
          <h1 className="page-title">Skills</h1>
          <p className="page-subtitle">Skills service is not available or the index is rebuilding. Try again in a moment.</p>
        </div>
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <button className="btn btn-primary" onClick={() => { setUnavailable(false); fetchSkills() }}>
            <i className="fas fa-redo" /> Retry
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="page">
      <style>{`
        .skills-header-actions {
          display: flex;
          gap: var(--spacing-sm);
          align-items: center;
          flex-wrap: wrap;
        }
        .skills-import-input {
          display: none;
        }
        .skills-grid {
          display: grid;
          grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
          gap: var(--spacing-md);
        }
        .skills-card-header {
          display: flex;
          justify-content: space-between;
          align-items: flex-start;
          margin-bottom: var(--spacing-sm);
        }
        .skills-card-name {
          font-size: 1.05rem;
          font-weight: 600;
          margin: 0;
          word-break: break-word;
        }
        .skills-card-desc {
          margin: 0 0 var(--spacing-md) 0;
          color: var(--color-text-secondary);
          font-size: 0.875rem;
        }
        .skills-card-actions {
          display: flex;
          gap: var(--spacing-xs);
          flex-wrap: wrap;
        }
        .skills-git-section {
          margin-bottom: var(--spacing-lg);
          padding: var(--spacing-md);
          background: var(--color-bg-secondary);
          border: 1px solid var(--color-border-default);
          border-radius: var(--radius-lg);
        }
        .skills-git-title {
          font-size: 1rem;
          font-weight: 600;
          margin: 0 0 var(--spacing-sm) 0;
        }
        .skills-git-desc {
          color: var(--color-text-secondary);
          font-size: 0.875rem;
          margin-bottom: var(--spacing-md);
        }
        .skills-git-form {
          display: flex;
          gap: var(--spacing-sm);
          flex-wrap: wrap;
          margin-bottom: var(--spacing-md);
        }
        .skills-git-form .input {
          flex: 1;
          min-width: 200px;
        }
        .skills-git-repo-item {
          display: flex;
          align-items: center;
          justify-content: space-between;
          flex-wrap: wrap;
          gap: var(--spacing-sm);
          padding: var(--spacing-sm) var(--spacing-md);
          margin-bottom: var(--spacing-xs);
          background: var(--color-bg-tertiary);
          border: 1px solid var(--color-border-subtle);
          border-radius: var(--radius-md);
        }
        .skills-git-repo-name {
          font-weight: 600;
        }
        .skills-git-repo-url {
          color: var(--color-text-secondary);
          font-size: 0.875rem;
          margin-left: var(--spacing-sm);
        }
        .skills-git-repo-actions {
          display: flex;
          gap: var(--spacing-xs);
        }
      `}</style>

      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div>
          <h1 className="page-title">Skills</h1>
          <p className="page-subtitle">Manage agent skills (reusable instructions and resources)</p>
        </div>
        <div className="skills-header-actions">
          <input
            type="text"
            className="input"
            placeholder="Search skills..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            style={{ width: '200px' }}
          />
          <button className="btn btn-primary" onClick={() => navigate('/app/skills/new')}>
            <i className="fas fa-plus" /> New skill
          </button>
          <label className="btn btn-secondary" style={{ cursor: 'pointer' }}>
            <i className="fas fa-file-import" /> {importing ? 'Importing...' : 'Import'}
            <input
              type="file"
              accept=".tar.gz"
              className="skills-import-input"
              onChange={handleImport}
              disabled={importing}
            />
          </label>
          <button
            className={`btn ${showGitRepos ? 'btn-primary' : 'btn-secondary'}`}
            onClick={() => setShowGitRepos((v) => !v)}
          >
            <i className="fas fa-code-branch" /> Git Repos
          </button>
        </div>
      </div>

      {showGitRepos && (
        <div className="skills-git-section">
          <h2 className="skills-git-title">
            <i className="fas fa-code-branch" style={{ marginRight: 'var(--spacing-xs)', color: 'var(--color-primary)' }} /> Git repositories
          </h2>
          <p className="skills-git-desc">
            Add Git repositories to pull skills from. Skills will appear in the list after sync.
          </p>
          <form onSubmit={addGitRepo} className="skills-git-form">
            <input
              type="url"
              className="input"
              placeholder="https://github.com/user/repo or git@github.com:user/repo.git"
              value={gitRepoUrl}
              onChange={(e) => setGitRepoUrl(e.target.value)}
            />
            <button type="submit" className="btn btn-primary" disabled={gitReposAction === 'add'}>
              {gitReposAction === 'add' ? <><i className="fas fa-spinner fa-spin" /> Adding...</> : 'Add repo'}
            </button>
          </form>
          {gitReposLoading ? (
            <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-md)' }}>
              <i className="fas fa-spinner fa-spin" style={{ fontSize: '1.5rem', color: 'var(--color-text-muted)' }} />
            </div>
          ) : gitRepos.length === 0 ? (
            <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem' }}>No Git repos configured. Add one above.</p>
          ) : (
            <div>
              {gitRepos.map((r) => (
                <div key={r.id} className="skills-git-repo-item">
                  <div>
                    <span className="skills-git-repo-name">{r.name || r.url}</span>
                    <span className="skills-git-repo-url">{r.url}</span>
                    {!r.enabled && <span className="badge" style={{ marginLeft: 'var(--spacing-sm)' }}>Disabled</span>}
                  </div>
                  <div className="skills-git-repo-actions">
                    <button
                      className="btn btn-secondary btn-sm"
                      onClick={() => syncGitRepo(r.id)}
                      disabled={gitReposAction === r.id}
                      title="Sync"
                    >
                      {gitReposAction === r.id ? <i className="fas fa-spinner fa-spin" /> : <><i className="fas fa-sync-alt" /> Sync</>}
                    </button>
                    <button
                      className="btn btn-secondary btn-sm"
                      onClick={() => toggleGitRepo(r.id)}
                      title={r.enabled ? 'Disable' : 'Enable'}
                    >
                      <i className={`fas fa-toggle-${r.enabled ? 'on' : 'off'}`} />
                    </button>
                    <button
                      className="btn btn-danger btn-sm"
                      onClick={() => deleteGitRepo(r.id)}
                      title="Remove repo"
                    >
                      <i className="fas fa-trash" />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <i className="fas fa-spinner fa-spin" style={{ fontSize: '2rem', color: 'var(--color-primary)' }} />
        </div>
      ) : skills.length === 0 && !userGroups ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-book" /></div>
          <h2 className="empty-state-title">No skills found</h2>
          <p className="empty-state-text">Create a skill or import one to get started.</p>
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'center' }}>
            <button className="btn btn-primary" onClick={() => navigate('/app/skills/new')}>
              <i className="fas fa-plus" /> Create skill
            </button>
            <label className="btn btn-secondary" style={{ cursor: 'pointer' }}>
              <i className="fas fa-file-import" /> Import
              <input
                type="file"
                accept=".tar.gz"
                className="skills-import-input"
                onChange={handleImport}
                disabled={importing}
              />
            </label>
          </div>
        </div>
      ) : (
        <>
        {userGroups && <h2 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>Your Skills</h2>}
        {skills.length === 0 ? (
          <p style={{ color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)' }}>You have no skills yet.</p>
        ) : (
        <div className="skills-grid">
          {skills.map((s) => (
            <div key={s.name} className="card">
              <div className="skills-card-header">
                <h3 className="skills-card-name">{s.name}</h3>
                {s.readOnly && <span className="badge">Read-only</span>}
              </div>
              <p className="skills-card-desc">
                {s.description || 'No description'}
              </p>
              <div className="skills-card-actions">
                {!s.readOnly && (
                  <button
                    className="btn btn-secondary btn-sm"
                    onClick={() => navigate(`/app/skills/edit/${encodeURIComponent(s.name)}`)}
                    title="Edit skill"
                  >
                    <i className="fas fa-edit" /> Edit
                  </button>
                )}
                {!s.readOnly && (
                  <button
                    className="btn btn-danger btn-sm"
                    onClick={() => deleteSkill(s.name)}
                    title="Delete skill"
                  >
                    <i className="fas fa-trash" /> Delete
                  </button>
                )}
                <button
                  className="btn btn-secondary btn-sm"
                  onClick={() => exportSkill(s.name)}
                  title="Export as .tar.gz"
                >
                  <i className="fas fa-download" /> Export
                </button>
              </div>
            </div>
          ))}
        </div>
        )}
        </>
      )}

      {userGroups && (
        <UserGroupSection
          title="Other Users' Skills"
          userGroups={userGroups}
          userMap={userMap}
          currentUserId={user?.id}
          itemKey="skills"
          renderGroup={(items) => (
            <div className="skills-grid">
              {(items || []).map((s) => (
                <div key={s.name} className="card">
                  <div className="skills-card-header">
                    <h3 className="skills-card-name">{s.name}</h3>
                    {s.readOnly && <span className="badge">Read-only</span>}
                  </div>
                  <p className="skills-card-desc">{s.description || 'No description'}</p>
                </div>
              ))}
            </div>
          )}
        />
      )}
    </div>
  )
}
