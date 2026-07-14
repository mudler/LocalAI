import { useState, useEffect, useRef, useCallback } from 'react'
import { loadClientMCPServers, addClientMCPServer, removeClientMCPServer } from '../utils/mcpClientStorage'

export default function ClientMCPDropdown({
  activeServerIds = [],
  onToggleServer,
  onServerAdded,
  onServerRemoved,
  connectionStatuses = {},
  getConnectedTools,
}) {
  const [open, setOpen] = useState(false)
  const [addDialog, setAddDialog] = useState(false)
  const [servers, setServers] = useState(() => loadClientMCPServers())
  const [url, setUrl] = useState('')
  const [name, setName] = useState('')
  const [authToken, setAuthToken] = useState('')
  const [useProxy, setUseProxy] = useState(true)
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return
    const handleClick = (e) => {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [open])

  const handleAdd = useCallback(() => {
    if (!url.trim()) return
    const headers = {}
    if (authToken.trim()) {
      headers.Authorization = `Bearer ${authToken.trim()}`
    }
    const server = addClientMCPServer({ name: name.trim() || undefined, url: url.trim(), headers, useProxy })
    setServers(loadClientMCPServers())
    setUrl('')
    setName('')
    setAuthToken('')
    setUseProxy(true)
    setAddDialog(false)
    if (onServerAdded) onServerAdded(server)
  }, [url, name, authToken, useProxy, onServerAdded])

  const handleRemove = useCallback((id) => {
    removeClientMCPServer(id)
    setServers(loadClientMCPServers())
    if (onServerRemoved) onServerRemoved(id)
  }, [onServerRemoved])

  const activeCount = activeServerIds.length

  return (
    <div className="chat-mcp-dropdown" ref={ref}>
      <button
        type="button"
        className={`btn btn-sm ${activeCount > 0 ? 'btn-primary' : 'btn-secondary'}`}
        title="Client-side MCP servers (browser connects directly)"
        onClick={() => setOpen(!open)}
      >
        <i className="fas fa-globe" /> Client MCP
        {activeCount > 0 && (
          <span className="chat-mcp-badge">{activeCount}</span>
        )}
      </button>
      {open && (
        <div className="chat-mcp-dropdown-menu" style={{ minWidth: '280px' }}>
          <div className="chat-mcp-dropdown-header">
            <span>Client MCP Servers</span>
            <button type="button" className="chat-mcp-select-all" onClick={() => setAddDialog(!addDialog)}>
              <i className="fas fa-plus" /> Add
            </button>
          </div>
          {addDialog && (
            <div style={{ padding: '8px 10px', borderBottom: '1px solid var(--color-border)' }}>
              <input
                type="text"
                className="input input-sm"
                placeholder="Server URL (e.g. https://mcp.example.com/sse)"
                value={url}
                onChange={e => setUrl(e.target.value)}
                style={{ width: '100%', marginBottom: 'var(--spacing-xs)' }}
              />
              <input
                type="text"
                className="input input-sm"
                placeholder="Name (optional)"
                value={name}
                onChange={e => setName(e.target.value)}
                style={{ width: '100%', marginBottom: 'var(--spacing-xs)' }}
              />
              <input
                type="password"
                className="input input-sm"
                placeholder="Auth token (optional)"
                value={authToken}
                onChange={e => setAuthToken(e.target.value)}
                style={{ width: '100%', marginBottom: 'var(--spacing-xs)' }}
              />
              <label style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '0.8rem', marginBottom: '6px' }}>
                <input type="checkbox" checked={useProxy} onChange={e => setUseProxy(e.target.checked)} />
                Use CORS proxy
              </label>
              <div style={{ display: 'flex', gap: 'var(--spacing-xs)', justifyContent: 'flex-end' }}>
                <button type="button" className="btn btn-sm btn-secondary" onClick={() => setAddDialog(false)}>Cancel</button>
                <button type="button" className="btn btn-sm btn-primary" onClick={handleAdd} disabled={!url.trim()}>Add</button>
              </div>
            </div>
          )}
          {servers.length === 0 && !addDialog ? (
            <div className="chat-mcp-dropdown-empty">No client MCP servers configured</div>
          ) : (
            servers.map(server => {
              const status = connectionStatuses[server.id]?.status || 'disconnected'
              const isActive = activeServerIds.includes(server.id)
              const connTools = getConnectedTools?.().find(c => c.serverId === server.id)
              return (
                <label key={server.id} className="chat-mcp-server-item">
                  <input
                    type="checkbox"
                    checked={isActive}
                    onChange={() => onToggleServer(server.id)}
                  />
                  <div className="chat-mcp-server-info" style={{ flex: 1 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                      <span className={`chat-client-mcp-status chat-client-mcp-status-${status}`} />
                      <span className="chat-mcp-server-name">{server.name}</span>
                      {server.headers?.Authorization && <i className="fas fa-lock" style={{ fontSize: '0.65rem', opacity: 0.5 }} title="Authenticated" />}
                    </div>
                    <span className="chat-mcp-server-tools">
                      {status === 'connecting' ? 'Connecting...' :
                       status === 'error' ? (connectionStatuses[server.id]?.error || 'Error') :
                       status === 'connected' && connTools ? `${connTools.tools.length} tools` :
                       server.url}
                    </span>
                  </div>
                  <button
                    className="btn btn-sm"
                    style={{ padding: '2px 6px', fontSize: '0.7rem', color: 'var(--color-error)' }}
                    onClick={(e) => { e.preventDefault(); e.stopPropagation(); handleRemove(server.id) }}
                    title="Remove server"
                  >
                    <i className="fas fa-trash" />
                  </button>
                </label>
              )
            })
          )}
        </div>
      )}
    </div>
  )
}
