import React from '../../../core/http/react-ui/node_modules/react/index.js'
import { createRoot } from '../../../core/http/react-ui/node_modules/react-dom/client.js'
import Button from '../../../core/http/react-ui/src/components/ui/Button.jsx'
import Switch from '../../../core/http/react-ui/src/components/ui/Switch.jsx'
import SideSheet from '../../../core/http/react-ui/src/components/SideSheet.jsx'
import { createAddon } from '../../../addons/asset-pipeline/ui/src/index.jsx'
async function rpc(data) {
 const response = await fetch('/rpc', { method: 'POST', body: JSON.stringify(data) })
 if (!response.ok) throw new Error(await response.text())
 return response.json()
}
const models = [
 { id: 'mock-review', capabilities: ['FLAG_CHAT'] },
 { id: 'mock-image', capabilities: ['FLAG_IMAGE'] },
 { id: 'mock-segment', capabilities: ['FLAG_DETECTION'] },
 { id: 'mock-trellis', capabilities: ['FLAG_3D'], three_d_operations: [{ id: 'generate', inputs: [{ type: 'image' }] }] },
 { id: 'mock-text-mesh', capabilities: ['FLAG_3D'], three_d_operations: [{ id: 'generate', inputs: [{ type: 'text' }] }] },
 { id: 'mock-fail', capabilities: ['FLAG_IMAGE'] },
 { id: 'mock-slow', capabilities: ['FLAG_IMAGE'] },
 { id: 'mock-remesh', capabilities: ['FLAG_3D'], three_d_operations: [{ id: 'remesh', inputs: [{ name: 'mesh', type: 'mesh' }] }] },
 { id: 'mock-skin', capabilities: ['FLAG_3D_ANIMATION'], three_d_operations: [
   { id: 'rig', inputs: [{ name: 'mesh', type: 'mesh' }] },
   { id: 'skin', inputs: [{ name: 'mesh', type: 'mesh' }, { name: 'skeleton', type: 'mesh' }] },
   { id: 'skin_motion', inputs: [{ name: 'mesh', type: 'mesh' }, { name: 'skeleton', type: 'motion' }] },
   { id: 'retarget', inputs: [{ name: 'mesh', type: 'mesh' }, { name: 'motion', type: 'motion' }] },
 ] },
 { id: 'mock-motion', capabilities: ['FLAG_3D_ANIMATION'], three_d_operations: [{ id: 'animate', inputs: [{ name: 'prompt', type: 'text' }], parameters: [
   { name: 'frames', label: 'Frames (30 FPS)', type: 'integer', default: '150', min: 60, max: 150 },
   { name: 'steps', label: 'Sampling steps', type: 'integer', default: '100', min: 1, max: 1000 },
 ] }] },
]
let minted = 0
const sdk = {
 React, signal: new AbortController().signal, markDirty() {}, viewer: { isAdmin: () => true }, core: { fetch: (path, options) => fetch(path, options) },
 ui: { Button, SideSheet, Switch },
 hooks: { useModels: () => ({ models }) },
 resources: {
  list: () => rpc({ method: 'list' }),
  create: ({ arguments: args }) => rpc({ method: 'create', ...args }),
  apply: async (resource, body, options) => ({ result: await rpc({ method: body.tool === 'pipeline-upload' ? 'upload' : 'apply', resource, revision: body.revision.data, operation: options?.operationId || crypto.randomUUID(), ...body.arguments }) }),
 },
 api: { read: async ({ resourceId, arguments: args }) => ({ payload: await rpc({ method: 'inspect', resource: resourceId, ...args }) }) },
 files: {
  read: async ({ resourceId, arguments: args }) => ({ files: [{ id: `/artifact?resource=${encodeURIComponent(resourceId)}&id=${encodeURIComponent(args.artifact)}&handle=${++minted}` }] }),
  blob: async (id, options) => { const response = await fetch(id, options); if (!response.ok) throw new Error(await response.text()); return response.blob() },
 },
}
const { Component } = createAddon(sdk)
createRoot(document.getElementById('root')).render(React.createElement(Component))
