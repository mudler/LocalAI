import React, { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { tracesApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'

function formatDuration(ns) {
  if (!ns && ns !== 0) return '-'
  if (ns < 1000) return `${ns}ns`
  if (ns < 1_000_000) return `${(ns / 1000).toFixed(1)}µs`
  if (ns < 1_000_000_000) return `${(ns / 1_000_000).toFixed(1)}ms`
  return `${(ns / 1_000_000_000).toFixed(2)}s`
}

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
      ) : activeTab === 'api' ? (
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: '30px' }}></th>
                <th>Time</th>
                <th>Method</th>
                <th>Path</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {traces.map((trace, i) => (
                <React.Fragment key={i}>
                  <tr onClick={() => setExpandedRow(expandedRow === i ? null : i)} style={{ cursor: 'pointer' }}>
                    <td><i className={`fas fa-chevron-${expandedRow === i ? 'down' : 'right'}`} style={{ fontSize: '0.7rem' }} /></td>
                    <td>{trace.timestamp ? new Date(trace.timestamp).toLocaleTimeString() : '-'}</td>
                    <td><span className="badge badge-info">{trace.request?.method || '-'}</span></td>
                    <td style={{ fontFamily: 'JetBrains Mono, monospace', fontSize: '0.8125rem' }}>{trace.request?.path || '-'}</td>
                    <td><span className={`badge ${(trace.response?.status || 0) < 400 ? 'badge-success' : 'badge-error'}`}>{trace.response?.status || '-'}</span></td>
                  </tr>
                  {expandedRow === i && (
                    <tr>
                      <td colSpan="5">
                        <pre style={{ background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)', borderRadius: 'var(--radius-md)', fontSize: '0.75rem', overflow: 'auto', maxHeight: '300px' }}>
                          {JSON.stringify(trace, null, 2)}
                        </pre>
                      </td>
                    </tr>
                  )}
                </React.Fragment>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: '30px' }}></th>
                <th>Time</th>
                <th>Type</th>
                <th>Model</th>
                <th>Backend</th>
                <th>Duration</th>
                <th>Summary</th>
              </tr>
            </thead>
            <tbody>
              {traces.map((trace, i) => (
                <React.Fragment key={i}>
                  <tr onClick={() => setExpandedRow(expandedRow === i ? null : i)} style={{ cursor: 'pointer' }}>
                    <td><i className={`fas fa-chevron-${expandedRow === i ? 'down' : 'right'}`} style={{ fontSize: '0.7rem' }} /></td>
                    <td>{trace.timestamp ? new Date(trace.timestamp).toLocaleTimeString() : '-'}</td>
                    <td><span className="badge badge-info">{trace.type || '-'}</span></td>
                    <td style={{ fontFamily: 'JetBrains Mono, monospace', fontSize: '0.8125rem' }}>{trace.model_name || '-'}</td>
                    <td>{trace.backend || '-'}</td>
                    <td>{formatDuration(trace.duration)}</td>
                    <td style={{ maxWidth: '300px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {trace.error ? <span style={{ color: 'var(--color-error)' }}>{trace.error}</span> : (trace.summary || '-')}
                    </td>
                  </tr>
                  {expandedRow === i && (
                    <tr>
                      <td colSpan="7">
                        <pre style={{ background: 'var(--color-bg-primary)', padding: 'var(--spacing-sm)', borderRadius: 'var(--radius-md)', fontSize: '0.75rem', overflow: 'auto', maxHeight: '300px' }}>
                          {JSON.stringify(trace, null, 2)}
                        </pre>
                      </td>
                    </tr>
                  )}
                </React.Fragment>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
