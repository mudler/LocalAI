import { useState, useRef, useCallback, useEffect } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import { modelsApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import CodeEditor from '../components/CodeEditor'
import SearchableSelect from '../components/SearchableSelect'

const BACKENDS = [
  { value: '', label: 'Auto-detect (based on URI)' },
  { value: 'llama-cpp', label: 'llama-cpp' },
  { value: 'mlx', label: 'mlx' },
  { value: 'mlx-vlm', label: 'mlx-vlm' },
  { value: 'transformers', label: 'transformers' },
  { value: 'vllm', label: 'vllm' },
  { value: 'diffusers', label: 'diffusers' },
]

const URI_FORMATS = [
  {
    icon: 'fab fa-hubspot', color: 'var(--color-accent)', title: 'HuggingFace',
    examples: [
      { prefix: 'huggingface://', suffix: 'TheBloke/Llama-2-7B-Chat-GGUF', desc: 'Standard HuggingFace format' },
      { prefix: 'hf://', suffix: 'TheBloke/Llama-2-7B-Chat-GGUF', desc: 'Short HuggingFace format' },
      { prefix: 'https://huggingface.co/', suffix: 'TheBloke/Llama-2-7B-Chat-GGUF', desc: 'Full HuggingFace URL' },
    ],
  },
  {
    icon: 'fas fa-globe', color: 'var(--color-primary)', title: 'HTTP/HTTPS URLs',
    examples: [
      { prefix: 'https://', suffix: 'example.com/model.gguf', desc: 'Direct download from any HTTPS URL' },
    ],
  },
  {
    icon: 'fas fa-file', color: 'var(--color-warning)', title: 'Local Files',
    examples: [
      { prefix: 'file://', suffix: '/path/to/model.gguf', desc: 'Local file path (absolute)' },
      { prefix: '', suffix: '/path/to/model.yaml', desc: 'Direct local YAML config file' },
    ],
  },
  {
    icon: 'fas fa-box', color: '#22d3ee', title: 'OCI Registry',
    examples: [
      { prefix: 'oci://', suffix: 'registry.example.com/model:tag', desc: 'OCI container registry' },
      { prefix: 'ocifile://', suffix: '/path/to/image.tar', desc: 'Local OCI tarball file' },
    ],
  },
  {
    icon: 'fas fa-cube', color: '#818cf8', title: 'Ollama',
    examples: [
      { prefix: 'ollama://', suffix: 'llama2:7b', desc: 'Ollama model format' },
    ],
  },
  {
    icon: 'fas fa-code', color: '#f472b6', title: 'YAML Configuration Files',
    examples: [
      { prefix: '', suffix: 'https://example.com/model.yaml', desc: 'Remote YAML config file' },
      { prefix: 'file://', suffix: '/path/to/config.yaml', desc: 'Local YAML config file' },
    ],
  },
]

const DEFAULT_YAML = `name: my-model
backend: llama-cpp
parameters:
  model: /path/to/model.gguf
`

const hintStyle = { marginTop: '4px', fontSize: '0.75rem', color: 'var(--color-text-muted)' }

export default function ImportModel() {
  const navigate = useNavigate()
  const { addToast } = useOutletContext()

  const [isAdvancedMode, setIsAdvancedMode] = useState(false)
  const [importUri, setImportUri] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [showGuide, setShowGuide] = useState(false)
  const [yamlContent, setYamlContent] = useState(DEFAULT_YAML)
  const [estimate, setEstimate] = useState(null)
  const [jobProgress, setJobProgress] = useState(null)

  const [prefs, setPrefs] = useState({
    backend: '', name: '', description: '', quantizations: '',
    mmproj_quantizations: '', embeddings: false, type: '',
    pipeline_type: '', scheduler_type: '', enable_parameters: '', cuda: false,
  })
  const [customPrefs, setCustomPrefs] = useState([])

  const pollRef = useRef(null)

  useEffect(() => {
    return () => { if (pollRef.current) clearInterval(pollRef.current) }
  }, [])

  const updatePref = (key, value) => setPrefs(p => ({ ...p, [key]: value }))
  const addCustomPref = () => setCustomPrefs(p => [...p, { key: '', value: '' }])
  const removeCustomPref = (i) => setCustomPrefs(p => p.filter((_, idx) => idx !== i))
  const updateCustomPref = (i, field, value) => {
    setCustomPrefs(p => p.map((item, idx) => idx === i ? { ...item, [field]: value } : item))
  }

  const startJobPolling = useCallback((jobId) => {
    if (pollRef.current) clearInterval(pollRef.current)
    pollRef.current = setInterval(async () => {
      try {
        const data = await modelsApi.getJobStatus(jobId)
        if (data.processed || data.progress) {
          setJobProgress(data.message || data.progress || 'Processing...')
        }
        if (data.completed) {
          clearInterval(pollRef.current)
          pollRef.current = null
          setIsSubmitting(false)
          setJobProgress(null)
          addToast('Model imported successfully!', 'success')
          navigate('/app/manage')
        } else if (data.error || (data.message && data.message.startsWith('error:'))) {
          clearInterval(pollRef.current)
          pollRef.current = null
          setIsSubmitting(false)
          setJobProgress(null)
          let msg = 'Unknown error'
          if (typeof data.error === 'string') msg = data.error
          else if (data.error?.message) msg = data.error.message
          else if (data.message) msg = data.message
          if (msg.startsWith('error: ')) msg = msg.substring(7)
          addToast(`Import failed: ${msg}`, 'error')
        }
      } catch (err) {
        console.error('Error polling job status:', err)
      }
    }, 1000)
  }, [addToast, navigate])

  const handleSimpleImport = async () => {
    if (!importUri.trim()) { addToast('Please enter a model URI', 'error'); return }
    setIsSubmitting(true)
    setEstimate(null)
    try {
      const prefsObj = {}
      if (prefs.backend) prefsObj.backend = prefs.backend
      if (prefs.name.trim()) prefsObj.name = prefs.name.trim()
      if (prefs.description.trim()) prefsObj.description = prefs.description.trim()
      if (prefs.quantizations.trim()) prefsObj.quantizations = prefs.quantizations.trim()
      if (prefs.mmproj_quantizations.trim()) prefsObj.mmproj_quantizations = prefs.mmproj_quantizations.trim()
      if (prefs.embeddings) prefsObj.embeddings = 'true'
      if (prefs.type.trim()) prefsObj.type = prefs.type.trim()
      if (prefs.pipeline_type.trim()) prefsObj.pipeline_type = prefs.pipeline_type.trim()
      if (prefs.scheduler_type.trim()) prefsObj.scheduler_type = prefs.scheduler_type.trim()
      if (prefs.enable_parameters.trim()) prefsObj.enable_parameters = prefs.enable_parameters.trim()
      if (prefs.cuda) prefsObj.cuda = true
      customPrefs.forEach(cp => {
        if (cp.key.trim() && cp.value.trim()) prefsObj[cp.key.trim()] = cp.value.trim()
      })

      const result = await modelsApi.importUri({
        uri: importUri.trim(),
        preferences: Object.keys(prefsObj).length > 0 ? prefsObj : null,
      })

      const hasSize = result.estimated_size_display && result.estimated_size_display !== '0 B'
      const hasVram = result.estimated_vram_display && result.estimated_vram_display !== '0 B'
      if (hasSize || hasVram) {
        setEstimate({ sizeDisplay: result.estimated_size_display || '', vramDisplay: result.estimated_vram_display || '' })
      }

      const jobId = result.uuid || result.ID
      if (!jobId) throw new Error('No job ID returned from server')

      let msg = 'Import started! Tracking progress...'
      const parts = []
      if (hasSize) parts.push(`Size: ${result.estimated_size_display}`)
      if (hasVram) parts.push(`VRAM: ${result.estimated_vram_display}`)
      if (parts.length) msg += ` (${parts.join(' \u00b7 ')})`
      addToast(msg, 'success')
      startJobPolling(jobId)
    } catch (err) {
      addToast(`Failed to start import: ${err.message}`, 'error')
      setIsSubmitting(false)
    }
  }

  const handleAdvancedImport = async () => {
    if (!yamlContent.trim()) { addToast('Please enter YAML configuration', 'error'); return }
    setIsSubmitting(true)
    try {
      await modelsApi.importConfig(yamlContent, 'application/x-yaml')
      addToast('Model configuration imported successfully!', 'success')
      navigate('/app/manage')
    } catch (err) {
      addToast(`Import failed: ${err.message}`, 'error')
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <div className="page" style={{ maxWidth: '900px' }}>
      <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--spacing-sm)' }}>
        <div>
          <h1 className="page-title">Import New Model</h1>
          <p className="page-subtitle">
            {isAdvancedMode ? 'Configure your model settings using YAML' : 'Import a model from URI with preferences'}
          </p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
          <button className="btn btn-secondary" onClick={() => navigate('/app/pipeline-editor')}>
            <i className="fas fa-diagram-project" /> Create Pipeline Model
          </button>
          <button className="btn btn-secondary" onClick={() => setIsAdvancedMode(!isAdvancedMode)}>
            <i className={`fas ${isAdvancedMode ? 'fa-magic' : 'fa-code'}`} />
            {isAdvancedMode ? ' Simple Mode' : ' Advanced Mode'}
          </button>
          {!isAdvancedMode ? (
            <button className="btn btn-primary" onClick={handleSimpleImport} disabled={isSubmitting || !importUri.trim()}>
              {isSubmitting ? <><LoadingSpinner size="sm" /> Importing...</> : <><i className="fas fa-upload" /> Import Model</>}
            </button>
          ) : (
            <button className="btn btn-primary" onClick={handleAdvancedImport} disabled={isSubmitting}>
              {isSubmitting ? <><LoadingSpinner size="sm" /> Saving...</> : <><i className="fas fa-save" /> Create</>}
            </button>
          )}
        </div>
      </div>

      {/* Estimate banner */}
      {!isAdvancedMode && estimate && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-md)', borderColor: 'var(--color-primary)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', fontSize: '0.875rem', flexWrap: 'wrap' }}>
            <i className="fas fa-memory" style={{ color: 'var(--color-primary)' }} />
            <strong>Estimated requirements</strong>
            {estimate.sizeDisplay && estimate.sizeDisplay !== '0 B' && (
              <span><i className="fas fa-download" style={{ color: 'var(--color-primary)', marginRight: '4px' }} />Download: {estimate.sizeDisplay}</span>
            )}
            {estimate.vramDisplay && estimate.vramDisplay !== '0 B' && (
              <span><i className="fas fa-microchip" style={{ color: 'var(--color-primary)', marginRight: '4px' }} />VRAM: {estimate.vramDisplay}</span>
            )}
          </div>
        </div>
      )}

      {/* Job progress */}
      {jobProgress && (
        <div className="card" style={{ marginBottom: 'var(--spacing-md)', padding: 'var(--spacing-md)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', fontSize: '0.875rem' }}>
            <LoadingSpinner size="sm" />
            <span>{jobProgress}</span>
          </div>
        </div>
      )}

      {/* Simple Import Mode */}
      {!isAdvancedMode && (
        <div className="card" style={{ padding: 'var(--spacing-lg)' }}>
          <h2 style={{ fontSize: '1.25rem', fontWeight: 600, marginBottom: 'var(--spacing-md)', display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
            <i className="fas fa-link" style={{ color: 'var(--color-success)' }} />
            Import from URI
          </h2>

          {/* URI Input */}
          <div className="form-group">
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '4px' }}>
              <label className="form-label" style={{ marginBottom: 0 }}>
                <i className="fas fa-link" style={{ marginRight: '6px' }} />Model URI
              </label>
              <a href="https://huggingface.co/models?search=gguf&sort=trending" target="_blank" rel="noreferrer"
                className="btn btn-secondary" style={{ fontSize: '0.7rem', padding: '3px 8px' }}>
                Search GGUF on HF <i className="fas fa-external-link-alt" style={{ marginLeft: '4px' }} />
              </a>
            </div>
            <input
              className="input"
              type="text"
              value={importUri}
              onChange={(e) => setImportUri(e.target.value)}
              placeholder="huggingface://TheBloke/Llama-2-7B-Chat-GGUF or https://example.com/model.gguf"
              disabled={isSubmitting}
            />
            <p style={hintStyle}>Enter the URI or path to the model file you want to import</p>

            {/* URI format guide */}
            <button
              onClick={() => setShowGuide(!showGuide)}
              style={{ marginTop: 'var(--spacing-sm)', background: 'none', border: 'none', color: 'var(--color-text-secondary)', cursor: 'pointer', fontSize: '0.8125rem', display: 'flex', alignItems: 'center', gap: '6px', padding: 0 }}
            >
              <i className={`fas ${showGuide ? 'fa-chevron-down' : 'fa-chevron-right'}`} />
              <i className="fas fa-info-circle" />
              Supported URI Formats
            </button>
            {showGuide && (
              <div style={{ marginTop: 'var(--spacing-sm)', padding: 'var(--spacing-md)', background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-default)', borderRadius: 'var(--radius-md)' }}>
                {URI_FORMATS.map((fmt, i) => (
                  <div key={i} style={{ marginBottom: i < URI_FORMATS.length - 1 ? 'var(--spacing-md)' : 0 }}>
                    <h4 style={{ fontSize: '0.8125rem', fontWeight: 600, marginBottom: '6px', display: 'flex', alignItems: 'center', gap: '6px' }}>
                      <i className={fmt.icon} style={{ color: fmt.color }} />
                      {fmt.title}
                    </h4>
                    <div style={{ paddingLeft: '20px', fontSize: '0.75rem', fontFamily: 'monospace' }}>
                      {fmt.examples.map((ex, j) => (
                        <div key={j} style={{ marginBottom: '4px' }}>
                          <code style={{ color: 'var(--color-success)' }}>{ex.prefix}</code>
                          <span style={{ color: 'var(--color-text-secondary)' }}>{ex.suffix}</span>
                          <p style={{ color: 'var(--color-text-muted)', marginTop: '1px', fontFamily: 'inherit' }}>{ex.desc}</p>
                        </div>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Preferences */}
          <div style={{ marginTop: 'var(--spacing-lg)' }}>
            <div style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-sm)' }}>
              <i className="fas fa-cog" style={{ marginRight: '6px' }} />Preferences (Optional)
            </div>

            <div style={{ padding: 'var(--spacing-md)', background: 'var(--color-bg-primary)', border: '1px solid var(--color-border-default)', borderRadius: 'var(--radius-md)' }}>
              <h3 style={{ fontSize: '0.8125rem', fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-md)', display: 'flex', alignItems: 'center', gap: '6px' }}>
                <i className="fas fa-star" style={{ color: 'var(--color-warning)' }} />
                Common Preferences
              </h3>

              <div style={{ display: 'grid', gap: 'var(--spacing-md)' }}>
                <div className="form-group" style={{ marginBottom: 0 }}>
                  <label className="form-label"><i className="fas fa-server" style={{ marginRight: '6px' }} />Backend</label>
                  <SearchableSelect
                    value={prefs.backend}
                    onChange={(v) => updatePref('backend', v)}
                    options={BACKENDS.filter(b => b.value !== '')}
                    allOption="Auto-detect (based on URI)"
                    placeholder="Auto-detect (based on URI)"
                    searchPlaceholder="Search backends..."
                    disabled={isSubmitting}
                  />
                  <p style={hintStyle}>Force a specific backend. Leave empty to auto-detect from URI.</p>
                </div>

                <div className="form-group" style={{ marginBottom: 0 }}>
                  <label className="form-label"><i className="fas fa-tag" style={{ marginRight: '6px' }} />Model Name</label>
                  <input className="input" type="text" value={prefs.name} onChange={e => updatePref('name', e.target.value)} placeholder="Leave empty to use filename" disabled={isSubmitting} />
                  <p style={hintStyle}>Custom name for the model. If empty, the filename will be used.</p>
                </div>

                <div className="form-group" style={{ marginBottom: 0 }}>
                  <label className="form-label"><i className="fas fa-align-left" style={{ marginRight: '6px' }} />Description</label>
                  <textarea className="textarea" rows={3} value={prefs.description} onChange={e => updatePref('description', e.target.value)} placeholder="Leave empty to use default description" disabled={isSubmitting} />
                  <p style={hintStyle}>Custom description for the model.</p>
                </div>

                <div className="form-group" style={{ marginBottom: 0 }}>
                  <label className="form-label"><i className="fas fa-layer-group" style={{ marginRight: '6px' }} />Quantizations</label>
                  <input className="input" type="text" value={prefs.quantizations} onChange={e => updatePref('quantizations', e.target.value)} placeholder="q4_k_m,q4_k_s,q3_k_m (comma-separated)" disabled={isSubmitting} />
                  <p style={hintStyle}>Preferred quantizations (comma-separated). Leave empty for default (q4_k_m).</p>
                </div>

                <div className="form-group" style={{ marginBottom: 0 }}>
                  <label className="form-label"><i className="fas fa-image" style={{ marginRight: '6px' }} />MMProj Quantizations</label>
                  <input className="input" type="text" value={prefs.mmproj_quantizations} onChange={e => updatePref('mmproj_quantizations', e.target.value)} placeholder="fp16,fp32 (comma-separated)" disabled={isSubmitting} />
                  <p style={hintStyle}>Preferred MMProj quantizations. Leave empty for default (fp16).</p>
                </div>

                <div>
                  <label style={{ display: 'flex', alignItems: 'center', gap: '8px', cursor: 'pointer' }}>
                    <input type="checkbox" checked={prefs.embeddings} onChange={e => updatePref('embeddings', e.target.checked)} disabled={isSubmitting} />
                    <span style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--color-text-secondary)' }}>
                      <i className="fas fa-vector-square" style={{ marginRight: '6px' }} />Embeddings
                    </span>
                  </label>
                  <p style={{ ...hintStyle, marginLeft: '28px' }}>Enable embeddings support for this model.</p>
                </div>

                <div className="form-group" style={{ marginBottom: 0 }}>
                  <label className="form-label"><i className="fas fa-tag" style={{ marginRight: '6px' }} />Model Type</label>
                  <input className="input" type="text" value={prefs.type} onChange={e => updatePref('type', e.target.value)} placeholder="AutoModelForCausalLM (for transformers backend)" disabled={isSubmitting} />
                  <p style={hintStyle}>Model type for transformers backend. Examples: AutoModelForCausalLM, SentenceTransformer, Mamba.</p>
                </div>

                {/* Diffusers-specific fields */}
                {prefs.backend === 'diffusers' && (
                  <>
                    <div className="form-group" style={{ marginBottom: 0 }}>
                      <label className="form-label"><i className="fas fa-stream" style={{ marginRight: '6px' }} />Pipeline Type</label>
                      <input className="input" type="text" value={prefs.pipeline_type} onChange={e => updatePref('pipeline_type', e.target.value)} placeholder="StableDiffusionPipeline" disabled={isSubmitting} />
                      <p style={hintStyle}>Pipeline type for diffusers backend.</p>
                    </div>
                    <div className="form-group" style={{ marginBottom: 0 }}>
                      <label className="form-label"><i className="fas fa-clock" style={{ marginRight: '6px' }} />Scheduler Type</label>
                      <input className="input" type="text" value={prefs.scheduler_type} onChange={e => updatePref('scheduler_type', e.target.value)} placeholder="k_dpmpp_2m (optional)" disabled={isSubmitting} />
                      <p style={hintStyle}>Scheduler type for diffusers backend. Examples: k_dpmpp_2m, euler_a, ddim.</p>
                    </div>
                    <div className="form-group" style={{ marginBottom: 0 }}>
                      <label className="form-label"><i className="fas fa-cogs" style={{ marginRight: '6px' }} />Enable Parameters</label>
                      <input className="input" type="text" value={prefs.enable_parameters} onChange={e => updatePref('enable_parameters', e.target.value)} placeholder="negative_prompt,num_inference_steps (comma-separated)" disabled={isSubmitting} />
                      <p style={hintStyle}>Enabled parameters for diffusers backend (comma-separated).</p>
                    </div>
                    <div>
                      <label style={{ display: 'flex', alignItems: 'center', gap: '8px', cursor: 'pointer' }}>
                        <input type="checkbox" checked={prefs.cuda} onChange={e => updatePref('cuda', e.target.checked)} disabled={isSubmitting} />
                        <span style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--color-text-secondary)' }}>
                          <i className="fas fa-microchip" style={{ marginRight: '6px' }} />CUDA
                        </span>
                      </label>
                      <p style={{ ...hintStyle, marginLeft: '28px' }}>Enable CUDA support for GPU acceleration.</p>
                    </div>
                  </>
                )}
              </div>
            </div>

            {/* Custom Preferences */}
            <div style={{ marginTop: 'var(--spacing-md)' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--spacing-sm)' }}>
                <span style={{ fontSize: '0.875rem', fontWeight: 500, color: 'var(--color-text-secondary)' }}>
                  <i className="fas fa-sliders-h" style={{ marginRight: '6px' }} />Custom Preferences
                </span>
                <button className="btn btn-secondary" onClick={addCustomPref} disabled={isSubmitting} style={{ fontSize: '0.75rem' }}>
                  <i className="fas fa-plus" /> Add Custom
                </button>
              </div>
              {customPrefs.map((cp, i) => (
                <div key={i} style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center', marginBottom: 'var(--spacing-xs)' }}>
                  <input className="input" type="text" value={cp.key} onChange={e => updateCustomPref(i, 'key', e.target.value)} placeholder="Key" disabled={isSubmitting} style={{ flex: 1 }} />
                  <span style={{ color: 'var(--color-text-secondary)' }}>:</span>
                  <input className="input" type="text" value={cp.value} onChange={e => updateCustomPref(i, 'value', e.target.value)} placeholder="Value" disabled={isSubmitting} style={{ flex: 1 }} />
                  <button className="btn btn-secondary" onClick={() => removeCustomPref(i)} disabled={isSubmitting} style={{ color: 'var(--color-error)' }}>
                    <i className="fas fa-trash" />
                  </button>
                </div>
              ))}
              <p style={hintStyle}>Add custom key-value pairs for advanced configuration.</p>
            </div>
          </div>
        </div>
      )}

      {/* Advanced YAML Editor Mode */}
      {isAdvancedMode && (
        <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
          <div style={{ padding: 'var(--spacing-md)', borderBottom: '1px solid var(--color-border-default)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <h2 style={{ fontSize: '1.125rem', fontWeight: 600, display: 'flex', alignItems: 'center', gap: '8px' }}>
              <i className="fas fa-code" style={{ color: '#d946ef' }} />
              YAML Configuration Editor
            </h2>
            <button className="btn btn-secondary" style={{ fontSize: '0.75rem' }} onClick={() => { navigator.clipboard.writeText(yamlContent); addToast('Copied to clipboard', 'success') }}>
              <i className="fas fa-copy" /> Copy
            </button>
          </div>
          <CodeEditor value={yamlContent} onChange={setYamlContent} disabled={isSubmitting} minHeight="calc(100vh - 400px)" />
        </div>
      )}
    </div>
  )
}
