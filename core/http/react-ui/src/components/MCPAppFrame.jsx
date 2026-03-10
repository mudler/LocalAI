import { useRef, useEffect, useState, useCallback } from 'react'
import { AppBridge, PostMessageTransport, buildAllowAttribute } from '@modelcontextprotocol/ext-apps/app-bridge'

export default function MCPAppFrame({ toolName, toolInput, toolResult, mcpClient, toolDefinition, appHtml, resourceMeta }) {
  const iframeRef = useRef(null)
  const bridgeRef = useRef(null)
  const [iframeHeight, setIframeHeight] = useState(200)
  const [error, setError] = useState(null)
  const initializedRef = useRef(false)

  const setupBridge = useCallback(async () => {
    if (!mcpClient || !iframeRef.current || initializedRef.current) return

    const iframe = iframeRef.current
    initializedRef.current = true

    try {
      const transport = new PostMessageTransport(iframe.contentWindow, iframe.contentWindow)
      const bridge = new AppBridge(
        mcpClient,
        { name: 'LocalAI', version: '1.0.0' },
        { openLinks: {}, serverTools: {}, serverResources: {}, logging: {} },
        { hostContext: { displayMode: 'inline' } }
      )

      bridge.oninitialized = () => {
        if (toolInput) bridge.sendToolInput({ arguments: toolInput })
        if (toolResult) bridge.sendToolResult(toolResult)
      }

      bridge.onsizechange = ({ height }) => {
        if (height && height > 0) setIframeHeight(Math.min(height, 600))
      }

      bridge.onopenlink = async ({ url }) => {
        window.open(url, '_blank', 'noopener,noreferrer')
        return {}
      }

      bridge.onmessage = async () => {
        return {}
      }

      bridge.onrequestdisplaymode = async () => {
        return { mode: 'inline' }
      }

      await bridge.connect(transport)
      bridgeRef.current = bridge
    } catch (err) {
      setError(`Bridge error: ${err.message}`)
    }
  }, [mcpClient, toolInput, toolResult])

  const handleIframeLoad = useCallback(() => {
    setupBridge()
  }, [setupBridge])

  // Send toolResult when it arrives after initialization
  useEffect(() => {
    if (bridgeRef.current && toolResult && initializedRef.current) {
      bridgeRef.current.sendToolResult(toolResult)
    }
  }, [toolResult])

  // Cleanup on unmount — only close the local transport, don't send
  // teardownResource which would kill server-side state and cause
  // "Connection closed" errors if the component remounts (e.g. when
  // streaming ends and ActivityGroup takes over from StreamingActivity).
  useEffect(() => {
    return () => {
      const bridge = bridgeRef.current
      if (bridge) {
        try { bridge.close() } catch (_) { /* ignore */ }
      }
    }
  }, [])

  if (!appHtml) return null

  const permissions = resourceMeta?.permissions
  const allowAttr = permissions ? buildAllowAttribute(permissions) : undefined

  return (
    <div className="mcp-app-frame-container">
      <iframe
        ref={iframeRef}
        srcDoc={appHtml}
        sandbox="allow-scripts allow-forms"
        allow={allowAttr}
        className="mcp-app-iframe"
        style={{ height: `${iframeHeight}px` }}
        onLoad={handleIframeLoad}
        title={`MCP App: ${toolName || 'unknown'}`}
      />
      {error && <div className="mcp-app-error">{error}</div>}
      {!mcpClient && (
        <div className="mcp-app-reconnect-overlay">
          Reconnect to MCP server to interact with this app
        </div>
      )}
    </div>
  )
}
