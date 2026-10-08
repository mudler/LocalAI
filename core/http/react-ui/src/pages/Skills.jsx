import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentCollectionsApi, skillsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import { useLibraryFacts } from '../hooks/useLibraryFacts'
import { useWideLayout } from '../hooks/useWideLayout'
// eslint-disable-next-line no-unused-vars
import UserGroupSection from '../components/UserGroupSection'
// eslint-disable-next-line no-unused-vars
import PageHeader from '../components/PageHeader'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import HomeUndoToast from '../components/home/HomeUndoToast'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { AddToMenu, UsedByStrip } from '../components/library/LibraryBits'
import { usedByLine } from '../utils/libraryText'
// eslint-disable-next-line no-unused-vars
import SimulateSheet from '../components/library/SimulateSheet'
import { estimateTokens, skillUsers, withSkill, withoutSkill } from '../utils/library'
import './library.css'

const SEARCH_DELAY = 250
const PREVIEW_LINES = 8

export default function Skills() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const { t } = useTranslation(['skills', 'library'])
  const { isAdmin, authEnabled, user } = useAuth()
  const userMap = useUserMap()
  const facts = useLibraryFacts()
  const wide = useWideLayout()

  const [skills, setSkills] = useState([])
  const [matches, setMatches] = useState(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [usage, setUsage] = useState('all')
  const [loading, setLoading] = useState(true)
  const [importing, setImporting] = useState(false)
  const [unavailable, setUnavailable] = useState(false)
  const [gitRepos, setGitRepos] = useState([])
  const [gitRepoUrl, setGitRepoUrl] = useState('')
  const [gitReposLoading, setGitReposLoading] = useState(false)
  const [gitReposAction, setGitReposAction] = useState(null)
  const [userGroups, setUserGroups] = useState(null)
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [sim, setSim] = useState(null)
  const [collections, setCollections] = useState([])
  const [undo, setUndo] = useState(null)
  const [busy, setBusy] = useState(false)

  const gitView = params.get('view') === 'git'
  const selectedUser = params.get('user_id') || undefined

  const select = useCallback((name, userId) => {
    const next = new URLSearchParams()
    if (name) next.set('skill', name)
    if (userId) next.set('user_id', userId)
    setParams(next)
  }, [setParams])

  const fetchSkills = useCallback(async () => {
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
      const data = await withTimeout(skillsApi.list(isAdmin && authEnabled))
      // Handle wrapped response (admin) or flat array (regular user)
      if (Array.isArray(data)) {
        setSkills(data)
        setUserGroups(null)
      } else {
        setSkills(Array.isArray(data.skills) ? data.skills : [])
        setUserGroups(data.user_groups || null)
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
  }, [addToast, isAdmin, authEnabled, t])

  useEffect(() => {
    fetchSkills()
  }, [fetchSkills])

  // Collections only feed the Simulate sheet. A server without them answers
  // with an error, which just leaves the list empty.
  useEffect(() => {
    agentCollectionsApi.list(false)
      .then(data => setCollections((Array.isArray(data?.collections) ? data.collections : []).map(c => (typeof c === 'string' ? c : c.name))))
      .catch(() => setCollections([]))
  }, [])

  // The server searches skills; the page keeps the full list for the "Used"
  // filters and for what "every skill" means, and narrows it to the matches.
  const searchRun = useRef(0)
  useEffect(() => {
    const q = searchQuery.trim()
    if (!q) { setMatches(null); return undefined }
    const id = ++searchRun.current
    const timer = setTimeout(async () => {
      try {
        const data = await skillsApi.search(q)
        if (id === searchRun.current) setMatches(new Set((Array.isArray(data) ? data : []).map(s => s.name)))
      } catch (err) {
        if (id === searchRun.current) {
          setMatches(new Set())
          addToast(err.message || t('toasts.loadFailed'), 'error')
        }
      }
    }, SEARCH_DELAY)
    return () => clearTimeout(timer)
  }, [searchQuery, addToast, t])

  const allNames = useMemo(() => skills.map(s => s.name), [skills])
  const usersOf = useMemo(() => {
    const map = new Map()
    for (const s of skills) map.set(s.name, skillUsers(facts.agents, s.name, allNames))
    return map
  }, [skills, facts.agents, allNames])
  const known = facts.status === 'ready'

  const visible = useMemo(() => skills.filter(s => {
    if (matches && !matches.has(s.name)) return false
    if (known && usage === 'used' && usersOf.get(s.name).length === 0) return false
    if (known && usage === 'unused' && usersOf.get(s.name).length > 0) return false
    return true
  }), [skills, matches, usage, known, usersOf])

  // Side by side, the first skill is open until another is picked. On a phone
  // nothing opens until the person taps one.
  const asked = params.get('skill') || ''
  const selectedName = asked || (wide && !selectedUser && !gitView ? (visible[0]?.name || '') : '')
  const selected = !selectedUser && !gitView ? skills.find(s => s.name === selectedName) : null
  const selectedOther = selectedUser && !gitView
    ? ((userGroups?.[selectedUser]?.skills || []).find(s => s.name === selectedName) || null)
    : null
  const open = gitView || selected || selectedOther

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
          select('')
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

  // ---- Attach and detach -------------------------------------------------
  const configOf = (agent) => facts.agents.find(a => a.name === agent)?.config || null

  const saveFor = async (agent, next) => {
    setBusy(true)
    try {
      await facts.saveConfig(agent, next)
      return true
    } catch (err) {
      addToast(t('library:toasts.updateFailed', { agent, message: err.message }), 'error')
      return false
    } finally {
      setBusy(false)
    }
  }

  const addToAgent = async (skill, agent) => {
    const config = configOf(agent)
    if (!config) { addToast(t('library:toasts.updateFailed', { agent, message: t('library:usedBy.unread') }), 'error'); return }
    const next = withSkill(config, skill.name, allNames)
    if (!next) return
    if (await saveFor(agent, next)) addToast(t('library:toasts.skillAdded', { name: skill.name, agent }), 'success')
  }

  const removeFromAgent = async (skill, agent) => {
    const config = configOf(agent)
    const result = config && withoutSkill(config, skill.name, allNames)
    if (!result) return
    if (!(await saveFor(agent, result.config))) return
    setUndo({
      id: Date.now(),
      message: t(result.switchedOff ? 'library:toasts.skillsOff' : 'library:toasts.skillRemoved', { name: skill.name, agent }),
      restore: async () => {
        if (await saveFor(agent, config)) addToast(t('library:toasts.undone'), 'success')
      },
    })
  }

  // ---- Git repositories --------------------------------------------------
  const loadGitRepos = useCallback(async () => {
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
  }, [addToast, t])

  useEffect(() => {
    if (gitView) loadGitRepos()
  }, [gitView, loadGitRepos])

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
      <div className="page page--wide lib-page">
        <PageHeader title={t('title')} supporting={t('unavailable.subtitle')} />
        <div className="dk-empty">
          <button className="dk-btn dk-btn--primary" onClick={() => { setUnavailable(false); fetchSkills() }}>
            <Icon name="refresh" /> {t('unavailable.retry')}
          </button>
        </div>
      </div>
    )
  }

  const importControl = (cls) => (
    <label className={cls} aria-busy={importing || undefined}>
      <Icon name="import" /> {importing ? t('actions.importing') : t('actions.import')}
      <input type="file" accept=".tar.gz" className="lib-file" onChange={handleImport} disabled={importing} />
    </label>
  )

  const empty = !loading && skills.length === 0 && !userGroups

  return (
    <div className="page page--wide lib-page" data-testid="skills-page">
      <PageHeader
        title={t('title')}
        supporting={t('subtitle')}
        actions={(
          <div className="lib-head-actions">
            <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setSim({ seed: null })} data-testid="open-simulate">
              <Icon name="flask" /> {t('library:simulate.button')}
            </button>
            {importControl('dk-btn dk-btn--secondary')}
            <button type="button" className="dk-btn dk-btn--primary" onClick={() => navigate('/app/skills/new')}>
              <Icon name="plus" /> {t('actions.newSkill')}
            </button>
          </div>
        )}
      />

      {loading ? (
        <div className="dk-empty" aria-busy="true"><Icon name="spinner" spin /></div>
      ) : empty ? (
        <div className="lib-empty" data-testid="skills-empty">
          <div className="dk-empty-icon"><Icon name="book" /></div>
          <h2>{t('empty.title')}</h2>
          <p>{t('empty.text')}</p>
          <ol className="lib-empty__steps">
            <li><span className="lib-step-n">1</span><span>{t('empty.stepWrite')}</span></li>
            <li><span className="lib-step-n">2</span><span>{t('empty.stepTry')}</span></li>
            <li><span className="lib-step-n">3</span><span>{t('empty.stepAdd')}</span></li>
          </ol>
          <div className="lib-empty__acts">
            <button className="dk-btn dk-btn--primary" onClick={() => navigate('/app/skills/new')}>
              <Icon name="plus" /> {t('actions.createSkill')}
            </button>
            {importControl('dk-btn dk-btn--secondary')}
            <button className="dk-btn dk-btn--secondary" onClick={() => setParams({ view: 'git' })}>
              <Icon name="git-branch" /> {t('actions.gitRepos')}
            </button>
          </div>
          {gitView && (
            <div className="lib-pane dk-card">
              <GitPane
                t={t} repos={gitRepos} loading={gitReposLoading} url={gitRepoUrl} setUrl={setGitRepoUrl}
                action={gitReposAction} onAdd={addGitRepo} onSync={syncGitRepo} onToggle={toggleGitRepo} onDelete={deleteGitRepo}
              />
            </div>
          )}
        </div>
      ) : (
        <div className="dk-split lib-split" data-view={open ? 'pane' : 'list'}>
          <div className="dk-split-rail lib-rail">
            <span className="dk-input-icon lib-find">
              <Icon name="search" className="dk-icon" />
              <input
                type="search"
                className="dk-input"
                placeholder={t('search.placeholder')}
                aria-label={t('search.label')}
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                data-testid="skills-search"
              />
            </span>
            <div className="lib-filters" role="group" aria-label={t('filters.label')}>
              {['all', 'used', 'unused'].map(f => (
                <button
                  key={f}
                  type="button"
                  className="dk-chip dk-chip--sm"
                  aria-pressed={usage === f}
                  disabled={f !== 'all' && !known}
                  onClick={() => setUsage(f)}
                  data-testid={`skills-filter-${f}`}
                >
                  {t(`filters.${f}`)}
                </button>
              ))}
            </div>

            {skills.length === 0 ? (
              <p className="lib-rail__none">{t('empty.noPersonal')}</p>
            ) : visible.length === 0 ? (
              <p className="lib-rail__none" data-testid="skills-none">{t('list.none')}</p>
            ) : (
              <div className="dk-list lib-rows" role="list" aria-label={t('list.label')}>
                {visible.map(s => (
                  <button
                    key={s.name}
                    type="button"
                    role="listitem"
                    className="dk-row"
                    aria-selected={!selectedUser && !gitView && selectedName === s.name}
                    onClick={() => select(s.name)}
                    data-testid={`skill-row-${s.name}`}
                  >
                    <span className="dk-row-lead"><Icon name="sparkles" /></span>
                    <span className="dk-row-main">
                      <span className="dk-row-title dk-mono">{s.name}</span>
                      <span className="dk-row-meta" data-testid={`skill-used-${s.name}`}>{usedByLine(t, usersOf.get(s.name), facts.status)}</span>
                    </span>
                    {s.readOnly && <span className="dk-row-end"><span className="dk-badge">{t('card.readOnly')}</span></span>}
                  </button>
                ))}
              </div>
            )}

            {userGroups && (
              <UserGroupSection
                title={t('sections.otherUsersSkills')}
                userGroups={userGroups}
                userMap={userMap}
                currentUserId={user?.id}
                itemKey="skills"
                renderGroup={(items, userId) => (
                  <div className="dk-list lib-others">
                    {(items || []).map(s => (
                      <button
                        key={s.name}
                        type="button"
                        className="dk-row"
                        aria-selected={selectedUser === userId && selectedName === s.name}
                        onClick={() => select(s.name, userId)}
                      >
                        <span className="dk-row-lead"><Icon name="sparkles" /></span>
                        <span className="dk-row-main"><span className="dk-row-title dk-mono">{s.name}</span></span>
                      </button>
                    ))}
                  </div>
                )}
              />
            )}

            <div className="lib-rail__foot">
              <button
                type="button"
                className="dk-btn dk-btn--ghost dk-btn--sm"
                aria-pressed={gitView}
                onClick={() => setParams(gitView ? {} : { view: 'git' })}
                data-testid="skills-git-toggle"
              >
                <Icon name="git-branch" /> {t('actions.gitRepos')}
              </button>
            </div>
          </div>

          <section className="dk-card lib-pane" aria-live="polite" data-testid="skill-pane">
            {gitView ? (
              <>
                <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm lib-back" onClick={() => select('')}>
                  <Icon name="arrow-left" /> {t('pane.back')}
                </button>
                <GitPane
                  t={t} repos={gitRepos} loading={gitReposLoading} url={gitRepoUrl} setUrl={setGitRepoUrl}
                  action={gitReposAction} onAdd={addGitRepo} onSync={syncGitRepo} onToggle={toggleGitRepo} onDelete={deleteGitRepo}
                />
              </>
            ) : selected || selectedOther ? (
              <SkillPane
                key={`${selectedUser || ''}/${selectedName}`}
                t={t}
                skill={selected || selectedOther}
                userId={selectedUser}
                users={selectedUser ? [] : usersOf.get(selectedName) || []}
                status={selectedUser ? 'other' : facts.status}
                agents={facts.agents}
                allNames={allNames}
                busy={busy}
                onBack={() => select('')}
                onEdit={(name, userId) => navigate(`/app/skills/edit/${encodeURIComponent(name)}${userId ? `?user_id=${encodeURIComponent(userId)}` : ''}`)}
                onExport={exportSkill}
                onDelete={deleteSkill}
                onAdd={(agent) => addToAgent(selected, agent)}
                onRemove={(agent) => removeFromAgent(selected, agent)}
                onTry={(skill) => setSim({ seed: { kind: 'skill', name: skill.name } })}
              />
            ) : (
              <div className="lib-pane__empty" data-testid="skill-pane-empty">
                <p>{t('pane.pick')}</p>
              </div>
            )}
          </section>
        </div>
      )}

      <SimulateSheet
        open={!!sim}
        onClose={() => setSim(null)}
        agents={facts.agents.filter(a => a.config)}
        agentsStatus={facts.status}
        skills={skills}
        collections={collections}
        seed={sim?.seed || null}
      />

      {undo && (
        <HomeUndoToast
          key={undo.id}
          message={undo.message}
          testId="library-undo-toast"
          undoLabel={t('library:toasts.undo')}
          dismissLabel={t('library:toasts.dismiss')}
          onUndo={() => { const { restore } = undo; setUndo(null); restore() }}
          onExpire={() => setUndo(null)}
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

// eslint-disable-next-line no-unused-vars
function SkillPane({ t, skill, userId, users, status, agents, allNames, busy, onBack, onEdit, onExport, onDelete, onAdd, onRemove, onTry }) {
  const [tab, setTab] = useState('overview')
  const [files, setFiles] = useState({ phase: 'idle', data: null })
  const tokens = estimateTokens(skill.content || '')
  const own = !userId
  const unused = own && status === 'ready' && users.length === 0

  useEffect(() => {
    if (tab !== 'files' || files.phase !== 'idle') return
    setFiles({ phase: 'loading', data: null })
    skillsApi.listResources(skill.name, userId)
      .then(data => setFiles({ phase: 'ready', data }))
      .catch(() => setFiles({ phase: 'failed', data: null }))
  }, [tab, files.phase, skill.name, userId])

  const metaEntries = Object.entries(skill.metadata || {})
  const tabs = ['overview', 'instructions', 'files']

  return (
    <>
      <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm lib-back" onClick={onBack}>
        <Icon name="arrow-left" /> {t('pane.back')}
      </button>

      <div className="lib-pane__head">
        <div className="lib-pane__title">
          <h2 className="lib-mono" data-testid="skill-title">{skill.name}</h2>
          <p className="lib-pane__sub">{skill.description || t('card.noDescription')}</p>
        </div>
        <div className="lib-pane__acts">
          {own && !unused && (
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => onTry(skill)} data-testid="skill-try">
              <Icon name="flask" /> {t('library:tryInContext')}
            </button>
          )}
          {!skill.readOnly && (
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => onEdit(skill.name, userId)} title={t('card.editTitle')}>
              <Icon name="edit" /> {t('actions.edit')}
            </button>
          )}
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => onExport(skill.name, userId)} title={t('card.exportTitle')}>
            <Icon name="download" /> {t('actions.export')}
          </button>
          {!skill.readOnly && (
            <button type="button" className="dk-btn dk-btn--danger dk-btn--sm" onClick={() => onDelete(skill.name, userId)} title={t('card.deleteTitle')}>
              <Icon name="trash" /> {t('actions.delete')}
            </button>
          )}
        </div>
      </div>

      <ul className="lib-facts">
        {skill.readOnly && <li className="lib-fact">{t('card.readOnly')}</li>}
        {skill.license && <li className="lib-fact">{t('pane.licenseFact', { license: skill.license })}</li>}
        <li className="lib-fact" data-testid="skill-tokens" title={t('pane.tokensTitle')}>{t('pane.tokens', { count: tokens })}</li>
      </ul>

      {own && (
        <>
          <UsedByStrip users={users} status={status} onRemove={onRemove} busy={busy}>
            <AddToMenu kind="skill" name={skill.name} skill={skill} agents={agents.filter(a => a.config)} allSkillNames={allNames} status={status} onAdd={onAdd} busy={busy} />
          </UsedByStrip>
          {unused && (
            <div className="lib-notused" data-testid="skill-not-used">
              <p>{t('library:notUsed.skill')}</p>
              <span className="lib-notused__acts">
                <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => onTry(skill)} data-testid="skill-try">
                  <Icon name="flask" /> {t('library:tryInContext')}
                </button>
              </span>
            </div>
          )}
        </>
      )}

      <div className="dk-tabs lib-tabs" role="tablist" aria-label={t('pane.tabs')}>
        {tabs.map(id => (
          <button
            key={id}
            type="button"
            role="tab"
            id={`skill-tab-${id}`}
            aria-controls={`skill-panel-${id}`}
            aria-selected={tab === id}
            tabIndex={tab === id ? 0 : -1}
            className="dk-tab"
            onClick={() => setTab(id)}
            onKeyDown={(e) => {
              const i = tabs.indexOf(id)
              if (e.key === 'ArrowRight') { e.preventDefault(); setTab(tabs[(i + 1) % tabs.length]); document.getElementById(`skill-tab-${tabs[(i + 1) % tabs.length]}`)?.focus() }
              if (e.key === 'ArrowLeft') { e.preventDefault(); setTab(tabs[(i + tabs.length - 1) % tabs.length]); document.getElementById(`skill-tab-${tabs[(i + tabs.length - 1) % tabs.length]}`)?.focus() }
            }}
          >
            {t(`pane.tab.${id}`)}
          </button>
        ))}
      </div>

      <div className="dk-tabpanel lib-section" role="tabpanel" id={`skill-panel-${tab}`} aria-labelledby={`skill-tab-${tab}`} tabIndex={0}>
        {tab === 'overview' && (
          <>
            {(skill['allowed-tools'] || skill.compatibility || metaEntries.length > 0) && (
              <dl className="lib-kv">
                {skill['allowed-tools'] && (<div className="lib-kv__pair"><dt>{t('pane.allowedTools')}</dt><dd className="lib-mono">{skill['allowed-tools']}</dd></div>)}
                {skill.compatibility && (<div className="lib-kv__pair"><dt>{t('pane.compatibility')}</dt><dd>{skill.compatibility}</dd></div>)}
                {metaEntries.map(([k, v]) => (<div key={k} className="lib-kv__pair"><dt>{k}</dt><dd>{v}</dd></div>))}
              </dl>
            )}
            {skill.content ? (
              <div className="lib-section">
                <h3 className="lib-eyebrow">{t('pane.startsWith')}</h3>
                <pre className="lib-pre lib-pre--preview" data-testid="skill-preview">{skill.content.split('\n').slice(0, PREVIEW_LINES).join('\n')}</pre>
                <button type="button" className="dk-link lib-more" onClick={() => setTab('instructions')}>{t('pane.showAll')}</button>
              </div>
            ) : <p className="lib-muted">{t('pane.noContent')}</p>}
            <p className="lib-muted">{t('pane.tokensExplain', { count: tokens })}</p>
          </>
        )}
        {tab === 'instructions' && (
          skill.content
            ? <pre className="lib-pre" data-testid="skill-content">{skill.content}</pre>
            : <p className="lib-muted">{t('pane.noContent')}</p>
        )}
        {tab === 'files' && (
          files.phase === 'loading' || files.phase === 'idle' ? (
            <p className="lib-muted" aria-busy="true">{t('pane.filesLoading')}</p>
          ) : files.phase === 'failed' ? (
            <p className="lib-note lib-note--error" role="alert">{t('pane.filesFailed')}</p>
          ) : (
            <FilesList t={t} data={files.data} />
          )
        )}
      </div>
    </>
  )
}

// eslint-disable-next-line no-unused-vars
function FilesList({ t, data }) {
  const groups = ['scripts', 'references', 'assets']
    .map(k => ({ key: k, items: Array.isArray(data?.[k]) ? data[k] : [] }))
    .filter(g => g.items.length > 0)
  if (groups.length === 0) return <p className="lib-muted" data-testid="skill-files-none">{t('pane.filesNone')}</p>
  return groups.map(g => (
    <div key={g.key} className="lib-section">
      <h3 className="lib-eyebrow">{t(`pane.files.${g.key}`)} <span className="lib-count">{g.items.length}</span></h3>
      <ul className="lib-files">
        {g.items.map(f => (
          <li key={f.path} className="lib-file-row">
            <Icon name="file-text" />
            <span className="lib-mono">{f.path}</span>
            {typeof f.size === 'number' && <span className="lib-muted">{f.size < 1024 ? `${f.size} B` : `${(f.size / 1024).toFixed(1)} KB`}</span>}
          </li>
        ))}
      </ul>
    </div>
  ))
}

// eslint-disable-next-line no-unused-vars
function GitPane({ t, repos, loading, url, setUrl, action, onAdd, onSync, onToggle, onDelete }) {
  return (
    <div className="lib-section" data-testid="skills-git">
      <div className="lib-pane__title">
        <h2>{t('git.title')}</h2>
        <p className="lib-pane__sub">{t('git.description')}</p>
      </div>
      <form onSubmit={onAdd} className="lib-git-form">
        <input
          type="url"
          className="dk-input"
          aria-label={t('git.urlLabel')}
          placeholder={t('git.urlPlaceholder')}
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
        <button type="submit" className="dk-btn dk-btn--primary" disabled={action === 'add'}>
          {action === 'add' ? <><Icon name="spinner" spin /> {t('actions.adding')}</> : t('actions.addRepo')}
        </button>
      </form>
      {loading ? (
        <p className="lib-muted" aria-busy="true"><Icon name="spinner" spin /></p>
      ) : repos.length === 0 ? (
        <p className="lib-muted">{t('git.noRepos')}</p>
      ) : (
        <div>
          {repos.map((r) => (
            <div key={r.id} className="lib-repo">
              <div className="lib-repo__main">
                <span className="lib-repo__name">
                  {r.name || r.url}
                  {!r.enabled && <span className="dk-badge lib-gap">{t('git.disabled')}</span>}
                </span>
                {r.name && <span className="lib-repo__url">{r.url}</span>}
              </div>
              <div className="lib-repo__acts">
                <button className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => onSync(r.id)} disabled={action === r.id} title={t('actions.sync')}>
                  {action === r.id ? <Icon name="spinner" spin /> : <><Icon name="refresh" /> {t('actions.sync')}</>}
                </button>
                <button className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => onToggle(r.id)} title={r.enabled ? t('actions.disable') : t('actions.enable')} aria-label={r.enabled ? t('actions.disable') : t('actions.enable')}>
                  <Icon name={`toggle-${r.enabled ? 'on' : 'off'}`} />
                </button>
                <button className="dk-btn dk-btn--danger dk-btn--sm" onClick={() => onDelete(r.id)} title={t('git.removeRepo')} aria-label={t('git.removeRepo')}>
                  <Icon name="trash" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
