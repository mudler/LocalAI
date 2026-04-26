import { generateId } from './format'

const STORAGE_KEY = 'localai_client_mcp_servers'

export function loadClientMCPServers() {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored) {
      const data = JSON.parse(stored)
      if (Array.isArray(data)) return data
    }
  } catch (_e) {
    // ignore
  }
  return []
}

export function saveClientMCPServers(servers) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(servers))
  } catch (_e) {
    // ignore
  }
}

export function addClientMCPServer({ name, url, headers, useProxy }) {
  const servers = loadClientMCPServers()
  const server = {
    id: generateId(),
    name: name || new URL(url).hostname,
    url,
    headers: headers || {},
    useProxy: useProxy !== false,
  }
  servers.push(server)
  saveClientMCPServers(servers)
  return server
}

export function removeClientMCPServer(id) {
  const servers = loadClientMCPServers().filter(s => s.id !== id)
  saveClientMCPServers(servers)
  return servers
}

export function updateClientMCPServer(id, updates) {
  const servers = loadClientMCPServers().map(s =>
    s.id === id ? { ...s, ...updates } : s
  )
  saveClientMCPServers(servers)
  return servers
}
