import { useState, useEffect } from 'react'
import { useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { modelsApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import LoadingSpinner from '../components/LoadingSpinner'
import CodeEditor from '../components/CodeEditor'

export default function ModelEditor() {
  const { name } = useParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const [config, setConfig] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!name) return
    modelsApi.getEditConfig(name).then(data => {
      setConfig(data?.config || '')
      setLoading(false)
    }).catch(err => {
      addToast(`Failed to load config: ${err.message}`, 'error')
      setLoading(false)
    })
  }, [name, addToast])

  const handleSave = async () => {
    setSaving(true)
    try {
      // Send raw YAML/text to the edit endpoint (not JSON-encoded)
      const response = await fetch(apiUrl(`/models/edit/${encodeURIComponent(name)}`), {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-yaml' },
        body: config,
      })
      const data = await response.json()
      if (!response.ok || data.success === false) {
        throw new Error(data.error || `HTTP ${response.status}`)
      }
      addToast('Config saved', 'success')
    } catch (err) {
      addToast(`Save failed: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <div className="page"><LoadingSpinner size="lg" /></div>

  return (
    <div className="page" style={{ maxWidth: '900px' }}>
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <h1 className="page-title">Model Editor</h1>
          <p className="page-subtitle">{decodeURIComponent(name)}</p>
        </div>
        <button className="btn btn-secondary" onClick={() => navigate('/app/manage')}>
          <i className="fas fa-arrow-left" /> Back
        </button>
      </div>

      <CodeEditor value={config} onChange={setConfig} minHeight="500px" />

      <div style={{ marginTop: 'var(--spacing-md)', display: 'flex', gap: 'var(--spacing-sm)' }}>
        <button className="btn btn-primary" onClick={handleSave} disabled={saving}>
          {saving ? <><LoadingSpinner size="sm" /> Saving...</> : <><i className="fas fa-save" /> Save</>}
        </button>
      </div>
    </div>
  )
}
