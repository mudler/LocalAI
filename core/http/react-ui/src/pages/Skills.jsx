import { useState, useEffect, useCallback } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { skillsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import UserGroupSection from '../components/UserGroupSection'
import ConfirmDialog from '../components/ConfirmDialog'

export default function Skills() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t } = useTranslation('skills')
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
  const [confirmDialog, setConfirmDialog] = useState(null)

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
        addToast(err.message || t('toasts.loadFailed'), 'error')
        setSkills([])
      }
    } finally {
      setLoading(false)
    }
  }, [searchQuery, addToast, isAdmin, authEnabled, t])

  useEffect(() => {
    fetchSkills()
  }, [fetchSkills])

  const deleteSkill = async (name, userId) => {
    setConfirmDialog({
      title: t('deleteDialog.title'),
      message: t('deleteDialog.message', { name }),
      confirmLabel: t('deleteDialog.confirm'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await skillsApi.delete(name, userId)
          addToast(t('toasts.deleted', { name }), 'success')
          fetchSkills()
        } catch (err) {
          addToast(err.message || t('toasts.deleteFailed'), 'error')
        }
      },
    })
  }

  const exportSkill = async (name, userId) => {
    try {
      const url = skillsApi.exportUrl(name, userId)
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
      addToast(t('toasts.exported', { name }), 'success')
    } catch (err) {
      addToast(err.message || t('toasts.exportFailed'), 'error')
    }
  }

  const handleImport = async (e) => {
    const file = e.target.files?.[0]
    if (!file) return
    setImporting(true)
    try {
      await skillsApi.import(file)
      addToast(t('toasts.imported', { file: file.name }), 'success')
      fetchSkills()
    } catch (err) {
      addToast(err.message || t('toasts.importFailed'), 'error')
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
      addToast(err.message || t('toasts.loadReposFailed'), 'error')
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
      addToast(t('toasts.repoAdded'), 'success')
    } catch (err) {
      addToast(err.message || t('toasts.addRepoFailed'), 'error')
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
      addToast(t('toasts.synced'), 'success')
    } catch (err) {
      addToast(err.message || t('toasts.syncFailed'), 'error')
    } finally {
      setGitReposAction(null)
    }
  }

  const toggleGitRepo = async (id) => {
    try {
      await skillsApi.toggleGitRepo(id)
      await loadGitRepos()
      fetchSkills()
      addToast(t('toasts.toggled'), 'success')
    } catch (err) {
      addToast(err.message || t('toasts.toggleFailed'), 'error')
    }
  }

  const deleteGitRepo = async (id) => {
    setConfirmDialog({
      title: t('removeRepoDialog.title'),
      message: t('removeRepoDialog.message'),
      confirmLabel: t('removeRepoDialog.confirm'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await skillsApi.deleteGitRepo(id)
          await loadGitRepos()
          fetchSkills()
          addToast(t('toasts.removed'), 'success')
        } catch (err) {
          addToast(err.message || t('toasts.removeFailed'), 'error')
        }
      },
    })
  }

  if (unavailable) {
    return (
      <div className="page page--wide">
        <div className="page-header">
          <h1 className="page-title">{t('title')}</h1>
          <p className="page-subtitle">{t('unavailable.subtitle')}</p>
        </div>
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <button className="btn btn-primary" onClick={() => { setUnavailable(false); fetchSkills() }}>
            <i className="fas fa-redo" /> {t('unavailable.retry')}
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="page page--wide">
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
          <h1 className="page-title">{t('title')}</h1>
          <p className="page-subtitle">{t('subtitle')}</p>
        </div>
        <div className="skills-header-actions">
          <input
            type="text"
            className="input"
            placeholder={t('search.placeholder')}
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            style={{ width: '200px' }}
          />
          <button className="btn btn-primary" onClick={() => navigate('/app/skills/new')}>
            <i className="fas fa-plus" /> {t('actions.newSkill')}
          </button>
          <label className="btn btn-secondary" style={{ cursor: 'pointer' }}>
            <i className="fas fa-file-import" /> {importing ? t('actions.importing') : t('actions.import')}
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
            <i className="fas fa-code-branch" /> {t('actions.gitRepos')}
          </button>
        </div>
      </div>

      {showGitRepos && (
        <div className="skills-git-section">
          <h2 className="skills-git-title">
            <i className="fas fa-code-branch" style={{ marginRight: 'var(--spacing-xs)', color: 'var(--color-primary)' }} /> {t('git.title')}
          </h2>
          <p className="skills-git-desc">
            {t('git.description')}
          </p>
          <form onSubmit={addGitRepo} className="skills-git-form">
            <input
              type="url"
              className="input"
              placeholder={t('git.urlPlaceholder')}
              value={gitRepoUrl}
              onChange={(e) => setGitRepoUrl(e.target.value)}
            />
            <button type="submit" className="btn btn-primary" disabled={gitReposAction === 'add'}>
              {gitReposAction === 'add' ? <><i className="fas fa-spinner fa-spin" /> {t('actions.adding')}</> : t('actions.addRepo')}
            </button>
          </form>
          {gitReposLoading ? (
            <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-md)' }}>
              <i className="fas fa-spinner fa-spin" style={{ fontSize: '1.5rem', color: 'var(--color-text-muted)' }} />
            </div>
          ) : gitRepos.length === 0 ? (
            <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem' }}>{t('git.noRepos')}</p>
          ) : (
            <div>
              {gitRepos.map((r) => (
                <div key={r.id} className="skills-git-repo-item">
                  <div>
                    <span className="skills-git-repo-name">{r.name || r.url}</span>
                    <span className="skills-git-repo-url">{r.url}</span>
                    {!r.enabled && <span className="badge" style={{ marginLeft: 'var(--spacing-sm)' }}>{t('git.disabled')}</span>}
                  </div>
                  <div className="skills-git-repo-actions">
                    <button
                      className="btn btn-secondary btn-sm"
                      onClick={() => syncGitRepo(r.id)}
                      disabled={gitReposAction === r.id}
                      title={t('actions.sync')}
                    >
                      {gitReposAction === r.id ? <i className="fas fa-spinner fa-spin" /> : <><i className="fas fa-sync-alt" /> {t('actions.sync')}</>}
                    </button>
                    <button
                      className="btn btn-secondary btn-sm"
                      onClick={() => toggleGitRepo(r.id)}
                      title={r.enabled ? t('actions.disable') : t('actions.enable')}
                    >
                      <i className={`fas fa-toggle-${r.enabled ? 'on' : 'off'}`} />
                    </button>
                    <button
                      className="btn btn-danger btn-sm"
                      onClick={() => deleteGitRepo(r.id)}
                      title={t('git.removeRepo')}
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
          <h2 className="empty-state-title">{t('empty.title')}</h2>
          <p className="empty-state-text">{t('empty.text')}</p>
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'center' }}>
            <button className="btn btn-primary" onClick={() => navigate('/app/skills/new')}>
              <i className="fas fa-plus" /> {t('actions.createSkill')}
            </button>
            <label className="btn btn-secondary" style={{ cursor: 'pointer' }}>
              <i className="fas fa-file-import" /> {t('actions.import')}
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
        {userGroups && <h2 style={{ fontSize: '1.1rem', fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>{t('sections.yourSkills')}</h2>}
        {skills.length === 0 ? (
          <p style={{ color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)' }}>{t('empty.noPersonal')}</p>
        ) : (
        <div className="skills-grid">
          {skills.map((s) => (
            <div key={s.name} className="card">
              <div className="skills-card-header">
                <h3 className="skills-card-name">{s.name}</h3>
                {s.readOnly && <span className="badge">{t('card.readOnly')}</span>}
              </div>
              <p className="skills-card-desc">
                {s.description || t('card.noDescription')}
              </p>
              <div className="skills-card-actions">
                {!s.readOnly && (
                  <button
                    className="btn btn-secondary btn-sm"
                    onClick={() => navigate(`/app/skills/edit/${encodeURIComponent(s.name)}`)}
                    title={t('card.editTitle')}
                  >
                    <i className="fas fa-edit" /> {t('actions.edit')}
                  </button>
                )}
                {!s.readOnly && (
                  <button
                    className="btn btn-danger btn-sm"
                    onClick={() => deleteSkill(s.name)}
                    title={t('card.deleteTitle')}
                  >
                    <i className="fas fa-trash" /> {t('actions.delete')}
                  </button>
                )}
                <button
                  className="btn btn-secondary btn-sm"
                  onClick={() => exportSkill(s.name)}
                  title={t('card.exportTitle')}
                >
                  <i className="fas fa-download" /> {t('actions.export')}
                </button>
              </div>
            </div>
          ))}
        </div>
        )}
        </>
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

      {userGroups && (
        <UserGroupSection
          title={t('sections.otherUsersSkills')}
          userGroups={userGroups}
          userMap={userMap}
          currentUserId={user?.id}
          itemKey="skills"
          renderGroup={(items, userId) => (
            <div className="skills-grid">
              {(items || []).map((s) => (
                <div key={s.name} className="card">
                  <div className="skills-card-header">
                    <h3 className="skills-card-name">{s.name}</h3>
                    {s.readOnly && <span className="badge">{t('card.readOnly')}</span>}
                  </div>
                  <p className="skills-card-desc">{s.description || t('card.noDescription')}</p>
                  <div className="skills-card-actions">
                    {!s.readOnly && (
                      <button
                        className="btn btn-secondary btn-sm"
                        onClick={() => navigate(`/app/skills/edit/${encodeURIComponent(s.name)}?user_id=${encodeURIComponent(userId)}`)}
                        title={t('card.editTitle')}
                      >
                        <i className="fas fa-edit" /> {t('actions.edit')}
                      </button>
                    )}
                    {!s.readOnly && (
                      <button
                        className="btn btn-danger btn-sm"
                        onClick={() => deleteSkill(s.name, userId)}
                        title={t('card.deleteTitle')}
                      >
                        <i className="fas fa-trash" /> {t('actions.delete')}
                      </button>
                    )}
                    <button
                      className="btn btn-secondary btn-sm"
                      onClick={() => exportSkill(s.name, userId)}
                      title={t('card.exportTitle')}
                    >
                      <i className="fas fa-download" /> {t('actions.export')}
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        />
      )}
    </div>
  )
}
