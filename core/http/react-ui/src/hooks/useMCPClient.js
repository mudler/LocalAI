import { useState, useRef, useCallback } from 'react'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js'
import { SSEClientTransport } from '@modelcontextprotocol/sdk/client/sse.js'
import { API_CONFIG } from '../utils/config'

function buildProxyUrl(targetUrl, useProxy = true) {
  if (!useProxy) return new URL(targetUrl)
  const base = window.location.origin
  return new URL(`${base}${API_CONFIG.endpoints.corsProxy}?url=${encodeURIComponent(targetUrl)}`)
}

export function useMCPClient() {
  const connectionsRef = useRef(new Map())
  const toolIndexRef = useRef(new Map())
  const [connectionStatuses, setConnectionStatuses] = useState({})

  const updateStatus = useCallback((serverId, status, error = null) => {
    setConnectionStatuses(prev => ({ ...prev, [serverId]: { status, error } }))
  }, [])

  const connect = useCallback(async (serverConfig) => {
    const { id, url, headers = {}, useProxy = true } = serverConfig
    if (connectionsRef.current.has(id)) return

    updateStatus(id, 'connecting')

    const proxyUrl = buildProxyUrl(url, useProxy)
    const transportHeaders = { ...headers }

    let client = null
    let transport = null

    // Try StreamableHTTP first, then SSE fallback
    for (const TransportClass of [StreamableHTTPClientTransport, SSEClientTransport]) {
      try {
        transport = new TransportClass(proxyUrl, { requestInit: { headers: transportHeaders } })
        client = new Client({ name: 'LocalAI-WebUI', version: '1.0.0' })
        await client.connect(transport)
        break
      } catch (err) {
        client = null
        transport = null
        if (TransportClass === SSEClientTransport) {
          updateStatus(id, 'error', err.message)
          return
        }
      }
    }

    if (!client) {
      updateStatus(id, 'error', 'Failed to connect with any transport')
      return
    }

    try {
      const { tools = [] } = await client.listTools()

      // Remove old tool index entries for this server
      for (const [toolName, sId] of toolIndexRef.current) {
        if (sId === id) toolIndexRef.current.delete(toolName)
      }

      for (const tool of tools) {
        toolIndexRef.current.set(tool.name, id)
      }

      connectionsRef.current.set(id, { client, transport, tools, serverConfig })
      updateStatus(id, 'connected')
    } catch (err) {
      try { await client.close() } catch (_) { /* ignore */ }
      updateStatus(id, 'error', err.message)
    }
  }, [updateStatus])

  const disconnect = useCallback(async (serverId) => {
    const conn = connectionsRef.current.get(serverId)
    if (!conn) return

    // Remove tool index entries
    for (const [toolName, sId] of toolIndexRef.current) {
      if (sId === serverId) toolIndexRef.current.delete(toolName)
    }

    try { await conn.client.close() } catch (_) { /* ignore */ }
    connectionsRef.current.delete(serverId)
    updateStatus(serverId, 'disconnected')
  }, [updateStatus])

  const disconnectAll = useCallback(async () => {
    const ids = [...connectionsRef.current.keys()]
    for (const id of ids) {
      await disconnect(id)
    }
  }, [disconnect])

  const getToolsForLLM = useCallback(() => {
    const tools = []
    for (const [, conn] of connectionsRef.current) {
      for (const tool of conn.tools) {
        tools.push({
          type: 'function',
          function: {
            name: tool.name,
            description: tool.description || '',
            parameters: tool.inputSchema || { type: 'object', properties: {} },
          },
        })
      }
    }
    return tools
  }, [])

  const isClientTool = useCallback((toolName) => {
    return toolIndexRef.current.has(toolName)
  }, [])

  const executeTool = useCallback(async (toolName, argumentsJson) => {
    const serverId = toolIndexRef.current.get(toolName)
    if (!serverId) return `Error: no MCP server found for tool "${toolName}"`

    const conn = connectionsRef.current.get(serverId)
    if (!conn) return `Error: server not connected for tool "${toolName}"`

    let args
    try {
      args = typeof argumentsJson === 'string' ? JSON.parse(argumentsJson) : argumentsJson
    } catch (_) {
      args = {}
    }

    try {
      const result = await conn.client.callTool({ name: toolName, arguments: args })
      return formatToolResult(result)
    } catch (err) {
      // Session might have expired — try reconnecting once
      if (err.message?.includes('404') || err.message?.includes('session')) {
        try {
          await disconnect(serverId)
          await connect(conn.serverConfig)
          const newConn = connectionsRef.current.get(serverId)
          if (newConn) {
            const result = await newConn.client.callTool({ name: toolName, arguments: args })
            return formatToolResult(result)
          }
        } catch (retryErr) {
          return `Error executing tool "${toolName}": ${retryErr.message}`
        }
      }
      return `Error executing tool "${toolName}": ${err.message}`
    }
  }, [connect, disconnect])

  const getConnectedTools = useCallback(() => {
    const result = []
    for (const [serverId, conn] of connectionsRef.current) {
      result.push({
        serverId,
        serverName: conn.serverConfig.name,
        tools: conn.tools.map(t => t.name),
      })
    }
    return result
  }, [])

  return {
    connect,
    disconnect,
    disconnectAll,
    getToolsForLLM,
    isClientTool,
    executeTool,
    connectionStatuses,
    getConnectedTools,
  }
}

function formatToolResult(result) {
  if (!result?.content) return ''
  const parts = []
  for (const item of result.content) {
    if (item.type === 'text') {
      parts.push(item.text)
    } else if (item.type === 'image') {
      parts.push(`[Image: ${item.mimeType || 'image'}]`)
    } else if (item.type === 'resource') {
      parts.push(item.resource?.text || JSON.stringify(item.resource))
    } else {
      parts.push(JSON.stringify(item))
    }
  }
  return parts.join('\n')
}
