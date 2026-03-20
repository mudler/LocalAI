import { useState, useEffect } from 'react'
import { useParams, useNavigate, useOutletContext } from 'react-router-dom'
import { modelsApi } from '../utils/api'
import SearchableModelSelect from '../components/SearchableModelSelect'
import LoadingSpinner from '../components/LoadingSpinner'
import { CAP_VAD, CAP_TRANSCRIPT, CAP_CHAT, CAP_TTS } from '../utils/capabilities'

const PIPELINE_FIELDS = [
  { key: 'vad', label: 'VAD Model', capability: CAP_VAD, icon: 'fas fa-microphone-lines', hint: 'Voice Activity Detection model' },
  { key: 'transcription', label: 'Transcription Model', capability: CAP_TRANSCRIPT, icon: 'fas fa-closed-captioning', hint: 'Speech-to-text model' },
  { key: 'llm', label: 'LLM Model', capability: CAP_CHAT, icon: 'fas fa-brain', hint: 'Language model for generating responses' },
  { key: 'tts', label: 'TTS Model', capability: CAP_TTS, icon: 'fas fa-volume-high', hint: 'Text-to-speech model' },
]

function buildPayload(formData) {
  const payload = {
    name: formData.name.trim(),
    pipeline: {
      vad: formData.vad.trim(),
      transcription: formData.transcription.trim(),
      llm: formData.llm.trim(),
      tts: formData.tts.trim(),
    },
  }
  if (formData.voice.trim()) {
    payload.tts = { voice: formData.voice.trim() }
  }
  return payload
}

export default function PipelineEditor() {
  const { name } = useParams()
  const navigate = useNavigate()
  const { addToast } = useOutletContext()
  const isEditMode = !!name

  const [formData, setFormData] = useState({
    name: '', vad: '', transcription: '', llm: '', tts: '', voice: '',
  })
  const [errors, setErrors] = useState({})
  const [loading, setLoading] = useState(isEditMode)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!isEditMode) return
    modelsApi.getConfigJson(name)
      .then(cfg => {
        setFormData({
          name: cfg.name || name,
          vad: cfg.pipeline?.vad || '',
          transcription: cfg.pipeline?.transcription || '',
          llm: cfg.pipeline?.llm || '',
          tts: cfg.pipeline?.tts || '',
          voice: cfg.tts?.voice || '',
        })
      })
      .catch(err => addToast(`Failed to load config: ${err.message}`, 'error'))
      .finally(() => setLoading(false))
  }, [name, isEditMode, addToast])

  const updateField = (field, value) => {
    setFormData(prev => ({ ...prev, [field]: value }))
    setErrors(prev => ({ ...prev, [field]: '' }))
  }

  const validate = () => {
    const errs = {}
    if (!isEditMode) {
      if (!formData.name.trim()) errs.name = 'Model name is required'
      else if (!/^[a-zA-Z0-9_.-]+$/.test(formData.name.trim())) errs.name = 'Only letters, numbers, hyphens, underscores, and dots'
    }
    for (const f of PIPELINE_FIELDS) {
      if (!formData[f.key].trim()) errs[f.key] = `${f.label} is required`
    }
    return errs
  }

  const handleSave = async () => {
    const errs = validate()
    if (Object.keys(errs).length > 0) {
      setErrors(errs)
      return
    }
    setSaving(true)
    try {
      const payload = buildPayload(formData)
      if (isEditMode) {
        await modelsApi.editConfig(name, payload)
        addToast('Pipeline model updated', 'success')
      } else {
        await modelsApi.importConfig(JSON.stringify(payload), 'application/json')
        addToast('Pipeline model created', 'success')
      }
      navigate('/app/talk')
    } catch (err) {
      addToast(`Save failed: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <div className="page"><LoadingSpinner size="lg" /></div>

  return (
    <div className="page" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
      <div style={{ width: '100%', maxWidth: '48rem' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 'var(--spacing-lg)' }}>
          <div>
            <h1 className="page-title">{isEditMode ? 'Edit Pipeline Model' : 'Create Pipeline Model'}</h1>
            <p className="page-subtitle">
              {isEditMode
                ? decodeURIComponent(name)
                : 'Configure a real-time voice pipeline (VAD + Transcription + LLM + TTS)'}
            </p>
          </div>
          <button className="btn btn-secondary" onClick={() => navigate('/app/talk')}>
            <i className="fas fa-arrow-left" style={{ marginRight: 'var(--spacing-xs)' }} /> Back
          </button>
        </div>

        <div className="card" style={{ padding: 'var(--spacing-lg)' }}>
          {/* Model Name */}
          <div className="form-group">
            <label className="form-label">
              <i className="fas fa-tag" style={{ color: 'var(--color-primary)', marginRight: 4 }} />
              Model Name {!isEditMode && <span style={{ color: 'var(--color-error)' }}>*</span>}
            </label>
            <input
              className="input"
              value={formData.name}
              onChange={e => updateField('name', e.target.value)}
              disabled={isEditMode}
              placeholder="my-pipeline-model"
              style={{ fontSize: '0.8125rem' }}
            />
            {errors.name && <p style={{ color: 'var(--color-error)', fontSize: '0.75rem', margin: '4px 0 0' }}>{errors.name}</p>}
          </div>

          {/* Model Type */}
          <div className="form-group">
            <label className="form-label">
              <i className="fas fa-layer-group" style={{ color: 'var(--color-primary)', marginRight: 4 }} />
              Model Type
            </label>
            <div>
              <span style={{
                display: 'inline-block',
                padding: '2px 10px',
                borderRadius: 'var(--radius-sm)',
                background: 'var(--color-primary-light)',
                color: 'var(--color-primary)',
                fontWeight: 600,
                fontSize: '0.8125rem',
              }}>
                pipeline
              </span>
            </div>
          </div>

          {/* Pipeline model selectors */}
          {PIPELINE_FIELDS.map(field => (
            <div className="form-group" key={field.key}>
              <label className="form-label">
                <i className={field.icon} style={{ color: 'var(--color-primary)', marginRight: 4 }} />
                {field.label} <span style={{ color: 'var(--color-error)' }}>*</span>
              </label>
              <SearchableModelSelect
                value={formData[field.key]}
                onChange={v => updateField(field.key, v)}
                capability={field.capability}
                placeholder={`Select or type ${field.label.toLowerCase()}...`}
                style={{ width: '100%' }}
              />
              {errors[field.key] && <p style={{ color: 'var(--color-error)', fontSize: '0.75rem', margin: '4px 0 0' }}>{errors[field.key]}</p>}
              <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.75rem', margin: '4px 0 0' }}>{field.hint}</p>
            </div>
          ))}

          {/* Voice (optional) */}
          <div className="form-group">
            <label className="form-label">
              <i className="fas fa-comment-dots" style={{ color: 'var(--color-primary)', marginRight: 4 }} />
              Voice <span style={{ color: 'var(--color-text-secondary)', fontWeight: 400 }}>(optional)</span>
            </label>
            <input
              className="input"
              value={formData.voice}
              onChange={e => updateField('voice', e.target.value)}
              placeholder="Voice name (e.g., en-us-1)"
              style={{ fontSize: '0.8125rem' }}
            />
            <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.75rem', margin: '4px 0 0' }}>
              Default voice for TTS output. Leave empty for model default.
            </p>
          </div>

          {/* Actions */}
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', marginTop: 'var(--spacing-lg)' }}>
            <button className="btn btn-primary" onClick={handleSave} disabled={saving}>
              {saving
                ? <><LoadingSpinner size="sm" /> Saving...</>
                : <><i className="fas fa-save" style={{ marginRight: 'var(--spacing-xs)' }} /> {isEditMode ? 'Save Changes' : 'Create Pipeline Model'}</>}
            </button>
            {isEditMode && (
              <button className="btn btn-secondary" onClick={() => navigate(`/app/model-editor/${encodeURIComponent(name)}`)}>
                <i className="fas fa-code" style={{ marginRight: 'var(--spacing-xs)' }} /> Edit Raw YAML
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
