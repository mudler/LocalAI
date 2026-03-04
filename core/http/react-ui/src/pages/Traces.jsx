import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { tracesApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'

export default function Traces() {
  const { addToast } = useOutletContext()
  const [activeTab, setActiveTab] = useState('api')
  const [traces, setTraces] = useState([])
  const [loading, setLoading] = useState(true)
  const [expandedRow, setExpandedRow] = useState(null)

  const fetchTraces = useCallback(async () => {
    try {
      setLoading(true)
      const data = activeTab === 'api'
        ? await tracesApi.get()
        : await tracesApi.getBackend()
      setTraces(Array.isArray(data) ? data : [])
    } catch (err) {
      addToast(`Failed to load traces: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [activeTab, addToast])

  useEffect(() => {
    fetchTraces()
  }, [fetchTraces])

  const handleClear = async () => {
    try {
      if (activeTab === 'api') await tracesApi.clear()
      else await tracesApi.clearBackend()
      setTraces([])
      addToast('Traces cleared', 'success')
    } catch (err) {
      addToast(`Failed to clear: ${err.message}`, 'error')
    }
  }

  const handleExport = () => {
    const blob = new Blob([JSON.stringify(traces, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `traces-${activeTab}-${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="page">
      <div className="page-header">
        <h1 className="page-title">Traces</h1>
        <p className="page-subtitle">Debug API and backend traces</p>
      </div>

      <div className="tabs">
        <button className={`tab ${activeTab === 'api' ? 'tab-active' : ''}`} onClick={() => setActiveTab('api')}>API Traces</button>
        <button className={`tab ${activeTab === 'backend' ? 'tab-active' : ''}`} onClick={() => setActiveTab('backend')}>Backend Traces</button>
      </div>

      <div style={{ display: 'flex', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)' }}>
        <button className="btn btn-secondary btn-sm" onClick={fetchTraces}><i className="fas fa-rotate" /> Refresh</button>
        <button className="btn btn-danger btn-sm" onClick={handleClear}><i className="fas fa-trash" /> Clear</button>
        <button className="btn btn-secondary btn-sm" onClick={handleExport} disabled={traces.length === 0}><i className="fas fa-download" /> Export</button>
      </div>

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}><LoadingSpinner size="lg" /></div>
      ) : traces.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-wave-square" /></div>
          <h2 className="empty-state-title">No traces</h2>
          <p className="empty-state-text">Traces will appear here as requests are made.</p>
        </div>
      ) : (
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: '30px' }}></th>
                <th>Time</th>
                <th>Method</th>
                <th>Path</th>
                <th>Status</th>
                <th>Duration</th>
              </tr>
            </thead>
            <tbody>
              {traces.map((trace, i) => (
                <>
                  <tr key={i} onClick={() => setExpandedRow(expandedRow === i ? null : i)} style={{ cursor: 'pointer' }}>
                    <td><i className={`fas fa-chevron-${expandedRow === i ? 'down' : 'right'}`} style={{ fontSize: '0.7rem' }} /></td>
                    <td>{trace.timestamp ? new Date(trace.timestamp).toLocaleTimeString() : '-'}</td>
                    <td><span className="badge badge-info">{trace.method || '-'}</span></td>
                    <td style={{ fontFamily: 'JetBrains Mono, monospace', fontSize: '0.8125rem' }}>{trace.path || trace.endpoint || '-'}</td>
                    <td><span className={`badge ${(trace.status || 0) < 400 ? 'badge-success' : 'badge-error'}`}>{trace.status || '-'}</span></td>
                    <td>{trace.duration || '-'}</td>
                  </tr>
                  {expandedRow === i && (
                    <tr key={`${i}-detail`}>
                      <td colSpan="6">
                        <pre style={{ background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)', borderRadius: 'var(--radius-md)', fontSize: '0.75rem', overflow: 'auto', maxHeight: '300px' }}>
                          {JSON.stringify(trace, null, 2)}
                        </pre>
                      </td>
                    </tr>
                  )}
                </>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
