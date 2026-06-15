import { useState, useEffect, useRef, useCallback } from 'react'
import { loadClientMCPServers, addClientMCPServer, removeClientMCPServer } from '../utils/mcpClientStorage'

export default function UnifiedMCPDropdown({
  // Server MCP props
  serverMCPAvailable = false,
  mcpServerList = [],
  mcpServersLoading = false,
  selectedServers = [],
  onToggleServer,
  onSelectAllServers,
  onFetchServers,
  // Client MCP props
  clientMCPActiveIds = [],
  onClientToggle,
  onClientAdded,
  onClientRemoved,
  connectionStatuses = {},
  getConnectedTools,
  // Prompts props (optional, Chat only)
  promptsAvailable = false,
  mcpPromptList = [],
  mcpPromptsLoading = false,
  onFetchPrompts,
  onSelectPrompt,
  promptArgsDialog = null,
  promptArgsValues = {},
  onPromptArgsChange,
  onPromptArgsSubmit,
  onPromptArgsCancel,
  // Resources props (optional, Chat only)
  resourcesAvailable = false,
  mcpResourceList = [],
  mcpResourcesLoading = false,
  onFetchResources,
  selectedResources = [],
  onToggleResource,
}) {
  const [open, setOpen] = useState(false)
  const [activeTab, setActiveTab] = useState(() => serverMCPAvailable ? 'servers' : 'client')
  const [addDialog, setAddDialog] = useState(false)
  const [clientServers, setClientServers] = useState(() => loadClientMCPServers())
  const [url, setUrl] = useState('')
  const [name, setName] = useState('')
  const [authToken, setAuthToken] = useState('')
  const [useProxy, setUseProxy] = useState(true)
  const ref = useRef(null)

  // Update default tab when serverMCPAvailable changes
  useEffect(() => {
    if (!serverMCPAvailable && activeTab === 'servers') {
      setActiveTab('client')
    }
  }, [serverMCPAvailable])

  // Click outside to close
  useEffect(() => {
    if (!open) return
    const handleClick = (e) => {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [open])

  const handleOpen = useCallback(() => {
    if (!open) {
      // Fetch data for default tab
      if (serverMCPAvailable && activeTab === 'servers' && onFetchServers) onFetchServers()
      else if (activeTab === 'prompts' && onFetchPrompts) onFetchPrompts()
      else if (activeTab === 'resources' && onFetchResources) onFetchResources()
    }
    setOpen(!open)
  }, [open, activeTab, serverMCPAvailable, onFetchServers, onFetchPrompts, onFetchResources])

  const switchTab = useCallback((tab) => {
    setActiveTab(tab)
    if (tab === 'servers' && onFetchServers) onFetchServers()
    else if (tab === 'prompts' && onFetchPrompts) onFetchPrompts()
    else if (tab === 'resources' && onFetchResources) onFetchResources()
  }, [onFetchServers, onFetchPrompts, onFetchResources])

  const handleAddClient = useCallback(() => {
    if (!url.trim()) return
    const headers = {}
    if (authToken.trim()) {
      headers.Authorization = `Bearer ${authToken.trim()}`
    }
    const server = addClientMCPServer({ name: name.trim() || undefined, url: url.trim(), headers, useProxy })
    setClientServers(loadClientMCPServers())
    setUrl('')
    setName('')
    setAuthToken('')
    setUseProxy(true)
    setAddDialog(false)
    if (onClientAdded) onClientAdded(server)
  }, [url, name, authToken, useProxy, onClientAdded])

  const handleRemoveClient = useCallback((id) => {
    removeClientMCPServer(id)
    setClientServers(loadClientMCPServers())
    if (onClientRemoved) onClientRemoved(id)
  }, [onClientRemoved])

  const totalBadge = (selectedServers?.length || 0) + (clientMCPActiveIds?.length || 0) + (selectedResources?.length || 0)

  const tabs = []
  if (serverMCPAvailable) tabs.push({ key: 'servers', label: 'Servers' })
  tabs.push({ key: 'client', label: 'Client' })
  if (promptsAvailable) tabs.push({ key: 'prompts', label: 'Prompts' })
  if (resourcesAvailable) tabs.push({ key: 'resources', label: 'Resources' })

  return (
    <div className="chat-mcp-dropdown" ref={ref}>
      <button
        type="button"
        className={`btn btn-sm ${totalBadge > 0 ? 'btn-primary' : 'btn-secondary'}`}
        title="MCP servers, prompts, and resources"
        onClick={handleOpen}
      >
        <i className="fas fa-plug" /> MCP
        {totalBadge > 0 && (
          <span className="chat-mcp-badge">{totalBadge}</span>
        )}
      </button>
      {open && (
        <div className="chat-mcp-dropdown-menu" style={{ minWidth: '300px' }}>
          {/* Tab bar */}
          <div className="unified-mcp-tabs">
            {tabs.map(tab => (
              <button
                key={tab.key}
                type="button"
                className={`unified-mcp-tab${activeTab === tab.key ? ' unified-mcp-tab-active' : ''}`}
                onClick={() => switchTab(tab.key)}
              >
                {tab.label}
              </button>
            ))}
          </div>

          {/* Servers tab */}
          {activeTab === 'servers' && serverMCPAvailable && (
            mcpServersLoading ? (
              <div className="chat-mcp-dropdown-loading"><i className="fas fa-spinner fa-spin" /> Loading servers...</div>
            ) : mcpServerList.length === 0 ? (
              <div className="chat-mcp-dropdown-empty">No MCP servers configured</div>
            ) : (
              <>
                <div className="chat-mcp-dropdown-header">
                  <span>MCP Servers</span>
                  <button type="button" className="chat-mcp-select-all" onClick={onSelectAllServers}>
                    {mcpServerList.every(s => selectedServers.includes(s.name)) ? 'Deselect all' : 'Select all'}
                  </button>
                </div>
                {mcpServerList.map(server => (
                  <label key={server.name} className="chat-mcp-server-item">
                    <input
                      type="checkbox"
                      checked={selectedServers.includes(server.name)}
                      onChange={() => onToggleServer(server.name)}
                    />
                    <div className="chat-mcp-server-info">
                      <span className="chat-mcp-server-name">{server.name}</span>
                      <span className="chat-mcp-server-tools">{server.tools?.length || 0} tools</span>
                    </div>
                  </label>
                ))}
              </>
            )
          )}

          {/* Client tab */}
          {activeTab === 'client' && (
            <>
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
                    <button type="button" className="btn btn-sm btn-primary" onClick={handleAddClient} disabled={!url.trim()}>Add</button>
                  </div>
                </div>
              )}
              {clientServers.length === 0 && !addDialog ? (
                <div className="chat-mcp-dropdown-empty">No client MCP servers configured</div>
              ) : (
                clientServers.map(server => {
                  const status = connectionStatuses[server.id]?.status || 'disconnected'
                  const isActive = clientMCPActiveIds.includes(server.id)
                  const connTools = getConnectedTools?.().find(c => c.serverId === server.id)
                  return (
                    <label key={server.id} className="chat-mcp-server-item">
                      <input
                        type="checkbox"
                        checked={isActive}
                        onChange={() => onClientToggle(server.id)}
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
                        onClick={(e) => { e.preventDefault(); e.stopPropagation(); handleRemoveClient(server.id) }}
                        title="Remove server"
                      >
                        <i className="fas fa-trash" />
                      </button>
                    </label>
                  )
                })
              )}
            </>
          )}

          {/* Prompts tab */}
          {activeTab === 'prompts' && promptsAvailable && (
            <>
              {promptArgsDialog ? (
                <>
                  <div className="chat-mcp-dropdown-header">
                    <span>{promptArgsDialog.title || promptArgsDialog.name}</span>
                  </div>
                  {promptArgsDialog.arguments.map(arg => (
                    <div key={arg.name} style={{ padding: '4px 10px' }}>
                      <label style={{ fontSize: '0.8rem', display: 'block', marginBottom: '2px' }}>
                        {arg.name}{arg.required ? ' *' : ''}
                      </label>
                      <input
                        type="text"
                        className="input input-sm"
                        style={{ width: '100%' }}
                        placeholder={arg.description || arg.name}
                        value={promptArgsValues[arg.name] || ''}
                        onChange={e => onPromptArgsChange(arg.name, e.target.value)}
                      />
                    </div>
                  ))}
                  <div style={{ padding: '6px 10px', display: 'flex', gap: '6px', justifyContent: 'flex-end' }}>
                    <button type="button" className="btn btn-sm btn-secondary" onClick={onPromptArgsCancel}>Cancel</button>
                    <button type="button" className="btn btn-sm btn-primary" onClick={onPromptArgsSubmit}>Apply</button>
                  </div>
                </>
              ) : mcpPromptsLoading ? (
                <div className="chat-mcp-dropdown-loading"><i className="fas fa-spinner fa-spin" /> Loading prompts...</div>
              ) : mcpPromptList.length === 0 ? (
                <div className="chat-mcp-dropdown-empty">No MCP prompts available</div>
              ) : (
                <>
                  <div className="chat-mcp-dropdown-header"><span>MCP Prompts</span></div>
                  {mcpPromptList.map(prompt => (
                    <div
                      key={prompt.name}
                      className="chat-mcp-server-item"
                      style={{ cursor: 'pointer', padding: '6px 10px' }}
                      onClick={() => onSelectPrompt(prompt)}
                    >
                      <div className="chat-mcp-server-info">
                        <span className="chat-mcp-server-name">{prompt.title || prompt.name}</span>
                        {prompt.description && (
                          <span className="chat-mcp-server-tools">{prompt.description}</span>
                        )}
                      </div>
                    </div>
                  ))}
                </>
              )}
            </>
          )}

          {/* Resources tab */}
          {activeTab === 'resources' && resourcesAvailable && (
            mcpResourcesLoading ? (
              <div className="chat-mcp-dropdown-loading"><i className="fas fa-spinner fa-spin" /> Loading resources...</div>
            ) : mcpResourceList.length === 0 ? (
              <div className="chat-mcp-dropdown-empty">No MCP resources available</div>
            ) : (
              <>
                <div className="chat-mcp-dropdown-header"><span>MCP Resources</span></div>
                {mcpResourceList.map(resource => (
                  <label key={resource.uri} className="chat-mcp-server-item">
                    <input
                      type="checkbox"
                      checked={selectedResources.includes(resource.uri)}
                      onChange={() => onToggleResource(resource.uri)}
                    />
                    <div className="chat-mcp-server-info">
                      <span className="chat-mcp-server-name">{resource.name}</span>
                      <span className="chat-mcp-server-tools">{resource.uri}</span>
                    </div>
                  </label>
                ))}
              </>
            )
          )}
        </div>
      )}

      <style>{`
        .unified-mcp-tabs {
          display: flex;
          border-bottom: 1px solid var(--color-border-subtle);
          padding: 0 4px;
        }
        .unified-mcp-tab {
          flex: 1;
          background: none;
          border: none;
          padding: 6px 8px;
          font-size: 0.75rem;
          font-family: inherit;
          color: var(--color-text-secondary);
          cursor: pointer;
          border-bottom: 2px solid transparent;
          transition: color var(--duration-fast), border-color var(--duration-fast);
        }
        .unified-mcp-tab:hover {
          color: var(--color-text-primary);
        }
        .unified-mcp-tab-active {
          color: var(--color-primary);
          border-bottom-color: var(--color-primary);
          font-weight: 500;
        }
      `}</style>
    </div>
  )
}
