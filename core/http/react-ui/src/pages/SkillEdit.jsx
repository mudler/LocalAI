import { useState, useEffect } from 'react'
import { useParams, useNavigate, useLocation, useOutletContext, useSearchParams } from 'react-router-dom'
import { skillsApi } from '../utils/api'

const RESOURCE_PREFIXES = ['scripts/', 'references/', 'assets/']
function isValidResourcePath(path) {
  return RESOURCE_PREFIXES.some((p) => path.startsWith(p)) && !path.includes('..')
}

function ResourceGroup({ title, icon, items, readOnly, pathPrefix, onView, onDelete, onUpload }) {
  const [collapsed, setCollapsed] = useState(false)

  return (
    <div style={{ marginBottom: 'var(--spacing-lg)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-sm)' }}>
        <h3
          style={{ margin: 0, fontWeight: 600, fontSize: '0.95rem', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}
          onClick={() => setCollapsed((v) => !v)}
        >
          <i className={`fas fa-chevron-${collapsed ? 'right' : 'down'}`} style={{ fontSize: '0.75rem', color: 'var(--color-text-muted)' }} />
          <i className={`fas fa-${icon}`} style={{ color: 'var(--color-primary)' }} /> {title}
          <span className="badge" style={{ marginLeft: 'var(--spacing-xs)' }}>{items.length}</span>
        </h3>
        {!readOnly && (
          <button className="btn btn-primary btn-sm" onClick={() => onUpload(pathPrefix)}>
            <i className="fas fa-upload" /> Upload
          </button>
        )}
      </div>
      {!collapsed && (
        items.length === 0 ? (
          <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem', padding: 'var(--spacing-sm)' }}>
            No {title.toLowerCase()} yet.
          </p>
        ) : (
          <div>
            {items.map((res) => (
              <div
                key={res.path}
                className="skilledit-resource-item"
              >
                <div style={{ minWidth: 0 }}>
                  <span style={{ fontWeight: 500 }}>{res.name}</span>
                  <span style={{ color: 'var(--color-text-secondary)', fontSize: '0.8rem', marginLeft: 'var(--spacing-sm)' }}>
                    {res.mime_type} &middot; {(res.size || 0).toLocaleString()} B
                  </span>
                </div>
                <div style={{ display: 'flex', gap: 'var(--spacing-xs)' }}>
                  <button className="btn btn-secondary btn-sm" onClick={() => onView(res)} title="View/Edit">
                    <i className="fas fa-edit" /> View/Edit
                  </button>
                  {!readOnly && (
                    <button className="btn btn-danger btn-sm" onClick={() => onDelete(res.path)} title="Delete">
                      <i className="fas fa-trash" />
                    </button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )
      )}
    </div>
  )
}

function ResourcesSection({ skillName, addToast }) {
  const [data, setData] = useState({ scripts: [], references: [], assets: [], readOnly: false })
  const [loading, setLoading] = useState(true)
  const [editor, setEditor] = useState({ open: false, path: '', name: '', content: '', readable: true, saving: false })
  const [upload, setUpload] = useState({ open: false, pathPrefix: 'assets/', file: null, pathInput: '', uploading: false })
  const [deletePath, setDeletePath] = useState(null)

  const load = async () => {
    setLoading(true)
    try {
      const res = await skillsApi.listResources(skillName)
      setData({
        scripts: res.scripts || [],
        references: res.references || [],
        assets: res.assets || [],
        readOnly: res.readOnly === true,
      })
    } catch (err) {
      addToast(err.message || 'Failed to load resources', 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [skillName])

  const handleView = async (res) => {
    setEditor({ open: true, path: res.path, name: res.name, content: '', readable: res.readable !== false, saving: false })
    if (res.readable !== false) {
      try {
        const json = await skillsApi.getResource(skillName, res.path, { json: true })
        const content = json.encoding === 'base64' && json.content ? atob(json.content) : (json.content || '')
        setEditor((e) => ({ ...e, content }))
      } catch (err) {
        addToast(err.message || 'Failed to load file', 'error')
      }
    }
  }

  const handleEditorSave = async () => {
    setEditor((e) => ({ ...e, saving: true }))
    try {
      await skillsApi.updateResource(skillName, editor.path, editor.content)
      addToast('Resource updated', 'success')
      setEditor((e) => ({ ...e, open: false }))
      load()
    } catch (err) {
      addToast(err.message || 'Update failed', 'error')
    } finally {
      setEditor((e) => ({ ...e, saving: false }))
    }
  }

  const handleUploadOpen = (pathPrefix) => {
    setUpload({ open: true, pathPrefix, file: null, pathInput: '', uploading: false })
  }

  const handleUploadSubmit = async () => {
    const path = upload.pathInput.trim() || (upload.file ? upload.pathPrefix + upload.file.name : '')
    if (!path || !upload.file) {
      addToast('Select a file and ensure path is set', 'error')
      return
    }
    if (!isValidResourcePath(path)) {
      addToast('Path must start with scripts/, references/, or assets/', 'error')
      return
    }
    setUpload((u) => ({ ...u, uploading: true }))
    try {
      await skillsApi.createResource(skillName, path, upload.file)
      addToast('Resource added', 'success')
      setUpload((u) => ({ ...u, open: false }))
      load()
    } catch (err) {
      addToast(err.message || 'Upload failed', 'error')
    } finally {
      setUpload((u) => ({ ...u, uploading: false }))
    }
  }

  const handleDeleteConfirm = async () => {
    if (!deletePath) return
    try {
      await skillsApi.deleteResource(skillName, deletePath)
      addToast('Resource deleted', 'success')
      setDeletePath(null)
      load()
    } catch (err) {
      addToast(err.message || 'Delete failed', 'error')
    }
  }

  return (
    <>
      <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>
        <i className="fas fa-folder" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} /> Resources
      </h3>
      <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem', marginBottom: 'var(--spacing-md)' }}>
        Scripts, references, and assets for this skill. Paths must start with scripts/, references/, or assets/.
      </p>
      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-md)' }}>
          <i className="fas fa-spinner fa-spin" style={{ fontSize: '1.5rem', color: 'var(--color-text-muted)' }} />
        </div>
      ) : (
        <>
          <ResourceGroup title="Scripts" icon="code" pathPrefix="scripts/" items={data.scripts} readOnly={data.readOnly} onView={handleView} onDelete={setDeletePath} onUpload={handleUploadOpen} />
          <ResourceGroup title="References" icon="book" pathPrefix="references/" items={data.references} readOnly={data.readOnly} onView={handleView} onDelete={setDeletePath} onUpload={handleUploadOpen} />
          <ResourceGroup title="Assets" icon="image" pathPrefix="assets/" items={data.assets} readOnly={data.readOnly} onView={handleView} onDelete={setDeletePath} onUpload={handleUploadOpen} />
        </>
      )}

      {editor.open && (
        <div className="skilledit-modal-overlay" onClick={() => !editor.saving && setEditor((e) => ({ ...e, open: false }))}>
          <div className="card skilledit-modal-card" style={{ maxWidth: '700px' }} onClick={(e) => e.stopPropagation()}>
            <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
              <i className="fas fa-edit" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} /> Edit {editor.name}
            </h3>
            {editor.readable ? (
              <>
                <textarea
                  className="input"
                  value={editor.content}
                  onChange={(e) => setEditor((x) => ({ ...x, content: e.target.value }))}
                  rows={14}
                  style={{ fontFamily: 'var(--font-mono)', fontSize: '0.875rem', marginBottom: 'var(--spacing-md)', width: '100%' }}
                />
                <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'flex-end' }}>
                  <button className="btn btn-secondary" onClick={() => setEditor((e) => ({ ...e, open: false }))}>Cancel</button>
                  <button className="btn btn-primary" disabled={editor.saving} onClick={handleEditorSave}>
                    {editor.saving ? <><i className="fas fa-spinner fa-spin" /> Saving...</> : <><i className="fas fa-save" /> Save</>}
                  </button>
                </div>
              </>
            ) : (
              <p style={{ color: 'var(--color-text-secondary)' }}>Binary file. Download via API or export skill.</p>
            )}
          </div>
        </div>
      )}

      {upload.open && (
        <div className="skilledit-modal-overlay" onClick={() => !upload.uploading && setUpload((u) => ({ ...u, open: false }))}>
          <div className="card skilledit-modal-card" style={{ maxWidth: '400px' }} onClick={(e) => e.stopPropagation()}>
            <h3 style={{ fontWeight: 600, marginBottom: 'var(--spacing-md)' }}>
              <i className="fas fa-upload" style={{ color: 'var(--color-primary)', marginRight: 'var(--spacing-xs)' }} /> Upload to {upload.pathPrefix}
            </h3>
            <div className="form-group">
              <label className="form-label">File</label>
              <input
                type="file"
                className="input"
                onChange={(e) => setUpload((u) => ({ ...u, file: e.target.files?.[0] || null }))}
              />
            </div>
            <div className="form-group">
              <label className="form-label">Path (default: {upload.pathPrefix} + filename)</label>
              <input
                type="text"
                className="input"
                placeholder={`${upload.pathPrefix}filename`}
                value={upload.pathInput}
                onChange={(e) => setUpload((u) => ({ ...u, pathInput: e.target.value }))}
              />
            </div>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'flex-end', marginTop: 'var(--spacing-md)' }}>
              <button className="btn btn-secondary" onClick={() => setUpload((u) => ({ ...u, open: false }))}>Cancel</button>
              <button className="btn btn-primary" disabled={upload.uploading || !upload.file} onClick={handleUploadSubmit}>
                {upload.uploading ? <><i className="fas fa-spinner fa-spin" /> Uploading...</> : <><i className="fas fa-upload" /> Upload</>}
              </button>
            </div>
          </div>
        </div>
      )}

      {deletePath && (
        <div className="skilledit-modal-overlay" onClick={() => setDeletePath(null)}>
          <div className="card skilledit-modal-card" style={{ maxWidth: '360px' }} onClick={(e) => e.stopPropagation()}>
            <p style={{ marginBottom: 'var(--spacing-md)' }}>Delete resource <strong>{deletePath}</strong>?</p>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)', justifyContent: 'flex-end' }}>
              <button className="btn btn-secondary" onClick={() => setDeletePath(null)}>Cancel</button>
              <button className="btn btn-danger" onClick={handleDeleteConfirm}>
                <i className="fas fa-trash" /> Delete
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}

export default function SkillEdit() {
  const { name: nameParam } = useParams()
  const location = useLocation()
  const isNew = location.pathname.endsWith('/new')
  const name = nameParam ? decodeURIComponent(nameParam) : undefined
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [searchParams] = useSearchParams()
  const userId = searchParams.get('user_id') || undefined
  const [loading, setLoading] = useState(!isNew)
  const [saving, setSaving] = useState(false)
  const [activeSection, setActiveSection] = useState('basic')
  const [form, setForm] = useState({
    name: '',
    description: '',
    content: '',
    license: '',
    compatibility: '',
    metadata: {},
    allowedTools: '',
  })

  useEffect(() => {
    if (isNew) {
      setLoading(false)
      return
    }
    if (name) {
      skillsApi.get(name, userId)
        .then((data) => {
          setForm({
            name: data.name || '',
            description: data.description || '',
            content: data.content || '',
            license: data.license || '',
            compatibility: data.compatibility || '',
            metadata: data.metadata || {},
            allowedTools: data['allowed-tools'] || '',
          })
        })
        .catch((err) => {
          addToast(err.message || 'Failed to load skill', 'error')
          navigate('/app/skills')
        })
        .finally(() => setLoading(false))
    }
  }, [isNew, name, navigate, addToast])

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!form.name.trim()) {
      addToast('Skill name is required', 'warning')
      return
    }
    if (!form.description.trim()) {
      addToast('Skill description is required', 'warning')
      return
    }
    setSaving(true)
    try {
      const payload = {
        name: form.name,
        description: form.description,
        content: form.content,
        license: form.license || undefined,
        compatibility: form.compatibility || undefined,
        metadata: Object.keys(form.metadata).length ? form.metadata : undefined,
        'allowed-tools': form.allowedTools || undefined,
      }
      if (isNew) {
        await skillsApi.create(payload)
        addToast('Skill created', 'success')
      } else {
        await skillsApi.update(name, { ...payload, name: undefined }, userId)
        addToast('Skill updated', 'success')
      }
      navigate('/app/skills')
    } catch (err) {
      addToast(err.message || 'Save failed', 'error')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="page page--narrow" style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
        <i className="fas fa-spinner fa-spin" style={{ fontSize: '2rem', color: 'var(--color-primary)' }} />
      </div>
    )
  }

  const sections = [
    { id: 'basic', label: 'Basic information', icon: 'fa-info-circle' },
    { id: 'content', label: 'Content', icon: 'fa-file-alt' },
    ...(!isNew && name ? [{ id: 'resources', label: 'Resources', icon: 'fa-folder' }] : []),
  ]

  return (
    <div className="page page--narrow">
      <style>{`
        .skilledit-back-link {
          display: inline-flex;
          align-items: center;
          gap: var(--spacing-xs);
          color: var(--color-text-secondary);
          font-size: 0.875rem;
          margin-bottom: var(--spacing-sm);
          cursor: pointer;
          text-decoration: none;
        }
        .skilledit-back-link:hover {
          color: var(--color-primary);
        }
        .skilledit-layout {
          display: flex;
          gap: var(--spacing-lg);
        }
        .skilledit-sidebar {
          flex-shrink: 0;
          width: 200px;
        }
        .skilledit-sidebar-nav {
          list-style: none;
          padding: 0;
          margin: 0;
        }
        .skilledit-sidebar-item {
          display: flex;
          align-items: center;
          gap: var(--spacing-sm);
          padding: var(--spacing-sm) var(--spacing-md);
          font-size: 0.875rem;
          color: var(--color-text-secondary);
          cursor: pointer;
          border-radius: var(--radius-md);
          border-left: 3px solid transparent;
          transition: all var(--duration-fast) var(--ease-default);
        }
        .skilledit-sidebar-item:hover {
          color: var(--color-text-primary);
          background: var(--color-primary-light);
        }
        .skilledit-sidebar-item.active {
          color: var(--color-primary);
          background: var(--color-primary-light);
          border-left-color: var(--color-primary);
          font-weight: 500;
        }
        .skilledit-form-area {
          flex: 1;
          min-width: 0;
        }
        .skilledit-section-title {
          font-weight: 600;
          font-size: 1rem;
          margin-bottom: var(--spacing-md);
        }
        .skilledit-field {
          margin-bottom: var(--spacing-md);
        }
        .skilledit-field label {
          display: block;
          font-size: 0.875rem;
          font-weight: 500;
          margin-bottom: var(--spacing-xs);
          color: var(--color-text-primary);
        }
        .skilledit-field .required {
          color: var(--color-error);
        }
        .skilledit-field .help-text {
          font-size: 0.8rem;
          color: var(--color-text-muted);
          margin-top: var(--spacing-xs);
        }
        .skilledit-form-actions {
          display: flex;
          gap: var(--spacing-sm);
          justify-content: flex-end;
          margin-top: var(--spacing-lg);
          padding-top: var(--spacing-md);
          border-top: 1px solid var(--color-border-subtle);
        }
        .skilledit-modal-overlay {
          position: fixed;
          inset: 0;
          background: var(--color-bg-overlay);
          z-index: 50;
          display: flex;
          align-items: center;
          justify-content: center;
          padding: var(--spacing-md);
        }
        .skilledit-modal-card {
          width: 100%;
          max-height: 90vh;
          display: flex;
          flex-direction: column;
          overflow: auto;
        }
        .skilledit-resource-item {
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
        @media (max-width: 700px) {
          .skilledit-layout {
            flex-direction: column;
          }
          .skilledit-sidebar {
            width: 100%;
          }
          .skilledit-sidebar-nav {
            display: flex;
            gap: var(--spacing-xs);
            overflow-x: auto;
          }
          .skilledit-sidebar-item {
            border-left: none;
            border-bottom: 3px solid transparent;
            white-space: nowrap;
          }
          .skilledit-sidebar-item.active {
            border-left-color: transparent;
            border-bottom-color: var(--color-primary);
          }
        }
      `}</style>

      <a className="skilledit-back-link" onClick={() => navigate('/app/skills')}>
        <i className="fas fa-arrow-left" /> Back to skills
      </a>
      <div className="page-header">
        <h1 className="page-title">
          <i className="fas fa-book" style={{ marginRight: 'var(--spacing-xs)' }} /> {isNew ? 'New skill' : `Edit: ${name}`}
        </h1>
      </div>

      <div className="card" style={{ marginTop: 'var(--spacing-md)' }}>
        <div className="skilledit-layout">
          <div className="skilledit-sidebar">
            <ul className="skilledit-sidebar-nav">
              {sections.map((s) => (
                <li
                  key={s.id}
                  className={`skilledit-sidebar-item ${activeSection === s.id ? 'active' : ''}`}
                  onClick={() => setActiveSection(s.id)}
                >
                  <i className={`fas ${s.icon}`} /> {s.label}
                </li>
              ))}
            </ul>
          </div>

          <div className="skilledit-form-area">
            <form onSubmit={handleSubmit} noValidate>
              <div style={{ display: activeSection === 'basic' ? 'block' : 'none' }}>
                <h3 className="skilledit-section-title">Basic information</h3>
                <div className="skilledit-field">
                  <label htmlFor="skill-name">Name (lowercase, hyphens only) <span className="required">*</span></label>
                  <input
                    id="skill-name"
                    type="text"
                    className="input"
                    value={form.name}
                    onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                    required
                    disabled={!isNew}
                    placeholder="my-skill"
                  />
                  {!isNew && <p className="help-text">Name cannot be changed after creation.</p>}
                </div>
                <div className="skilledit-field">
                  <label htmlFor="skill-desc">Description (required, max 1024 chars) <span className="required">*</span></label>
                  <textarea
                    id="skill-desc"
                    className="input"
                    value={form.description}
                    onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                    required
                    maxLength={1024}
                    rows={2}
                  />
                </div>
                <div className="skilledit-field">
                  <label htmlFor="skill-license">License (optional)</label>
                  <input
                    id="skill-license"
                    type="text"
                    className="input"
                    value={form.license}
                    onChange={(e) => setForm((f) => ({ ...f, license: e.target.value }))}
                  />
                </div>
                <div className="skilledit-field">
                  <label htmlFor="skill-compat">Compatibility (optional, max 500 chars)</label>
                  <input
                    id="skill-compat"
                    type="text"
                    className="input"
                    value={form.compatibility}
                    onChange={(e) => setForm((f) => ({ ...f, compatibility: e.target.value }))}
                    maxLength={500}
                  />
                </div>
                <div className="skilledit-field">
                  <label htmlFor="skill-allowed-tools">Allowed tools (optional)</label>
                  <input
                    id="skill-allowed-tools"
                    type="text"
                    className="input"
                    value={form.allowedTools}
                    onChange={(e) => setForm((f) => ({ ...f, allowedTools: e.target.value }))}
                    placeholder="tool1, tool2"
                  />
                </div>
              </div>

              <div style={{ display: activeSection === 'content' ? 'block' : 'none' }}>
                <h3 className="skilledit-section-title">Content</h3>
                <div className="skilledit-field">
                  <label htmlFor="skill-content">Skill content (markdown)</label>
                  <textarea
                    id="skill-content"
                    className="input"
                    value={form.content}
                    onChange={(e) => setForm((f) => ({ ...f, content: e.target.value }))}
                    rows={14}
                    style={{ fontFamily: 'var(--font-mono)', fontSize: '0.875rem' }}
                  />
                </div>
              </div>

              {activeSection === 'resources' && (
                <div>
                  {isNew || !name ? (
                    <div>
                      <h3 className="skilledit-section-title">Resources</h3>
                      <p style={{ color: 'var(--color-text-secondary)' }}>
                        Save the skill first to add scripts, references, and assets. After creating the skill, use this tab to upload files and manage resources.
                      </p>
                    </div>
                  ) : (
                    <ResourcesSection skillName={name} addToast={addToast} />
                  )}
                </div>
              )}

              <div className="skilledit-form-actions">
                <button type="button" className="btn btn-secondary" onClick={() => navigate('/app/skills')}>
                  <i className="fas fa-times" /> Cancel
                </button>
                <button type="submit" className="btn btn-primary" disabled={saving}>
                  <i className="fas fa-save" /> {saving ? 'Saving...' : (isNew ? 'Create skill' : 'Save changes')}
                </button>
              </div>
            </form>
          </div>
        </div>
      </div>
    </div>
  )
}
