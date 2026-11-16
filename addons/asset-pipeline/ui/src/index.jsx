// SPDX-License-Identifier: MIT
import { createGlbViewer } from '../../../internal/glb-viewer/renderer.js'
import { parseGlb } from '../../../studio/ui/src/utils/glb.js'
import { executionPreview, inputChoices, inputType, isProcessing, needsModel, readiness, ambiguousFailure, pointCoordinates, graphProblem } from './state.js'
import { same, nodeChanged, graphForNode, rebaseEdits, executionIssue, runAllIssue, workbookPlan } from './workbook.js'
import { createChainConnectors } from './ChainConnectors.jsx'
import { unpack } from './response.js'
import { createIcon } from './icons.js'
import { createTimeline } from './Timeline.jsx'
import { createAssetLoader } from './assets.js'
import { title, descriptions, compatibleModel, stageParameters } from './stages.js'
import { createAnimatedViewer, inspectGLB } from './animated-viewer.js'
import { ANIMATION_LABEL, isAnimationNode, readAnimationPreference, writeAnimationPreference, animationIssue } from './features.js'
import { createImagePicker } from './ImagePicker.jsx'
import { restoredSelection, rememberSelection } from './selection.js'

const copy = value => JSON.parse(JSON.stringify(value))
const operation = () => `op-${crypto.randomUUID()}`

export function createAddon(sdk) {
  const R = sdk.React, h = R.createElement, Fragment = R.Fragment
  const { useState, useEffect, useRef } = R
  const ChainConnectors = createChainConnectors(R)
  const Icon = createIcon(R)
  const Timeline = createTimeline(R)
  const ImagePicker = createImagePicker(sdk)
  const { Button, Switch } = sdk.ui
  const asset = createAssetLoader(sdk.files)

  function Mesh({ blob }) {
    const canvas = useRef(null)
    const viewer = useRef(null)
    const [error, setError] = useState(''), [stats, setStats] = useState('')
    const [playback, setPlayback] = useState(null), [quality, setQuality] = useState(null)
    useEffect(() => {
      let renderer, alive = true
      ;(async () => {
        try {
          const buffer = await blob.arrayBuffer()
          if (!alive) return
          const metadata = inspectGLB(buffer)
          setQuality(metadata.extras?.localai_remesh || null)
          if (metadata.skins?.length || metadata.animations?.length || !metadata.meshes?.length) {
            renderer = await createAnimatedViewer(canvas.current, buffer, { onState: state => { if (alive) setPlayback(state) }, onContextLost: () => { if (alive) setError('Graphics context lost. Unload and reload this preview.') } })
            if (!alive) { renderer.dispose(); return }
            viewer.current = renderer
            setStats(renderer.stats)
            return
          }
          const mesh = parseGlb(buffer)
          setStats(`${mesh.nTris.toLocaleString()} triangles · ${mesh.nVerts.toLocaleString()} vertices`)
          renderer = createGlbViewer(canvas.current, { onContextLost: () => setError('Graphics context lost. Unload and reload this preview.') })
          if (!renderer) throw new Error('WebGL2 is unavailable; the asset can still be downloaded.')
          renderer.setSpin(false)
          await renderer.setMesh(mesh)
        } catch (error) { if (alive) setError(error.message) }
      })()
      return () => { alive = false; viewer.current = null; renderer?.dispose() }
    }, [blob])
    return <div>{playback?.rig?.joints.length > 0 && <div className="ap-rig-controls"><label className="ap-check"><input type="checkbox" checked={playback.rig.skeleton} onChange={event => viewer.current?.setSkeleton(event.target.checked)} />Show skeleton (through mesh)</label><label>Surface view<select value={playback.rig.mode} onChange={event => viewer.current?.setDisplayMode(event.target.value)}><option value="mesh">Original mesh</option>{playback.rig.hasWeights && <option value="weights">Skin weights</option>}</select></label>{playback.rig.mode === 'weights' && <><label>Weight joint<select value={playback.rig.joint} onChange={event => viewer.current?.setJoint(+event.target.value)}>{playback.rig.joints.map((name, index) => <option key={index} value={index}>{name}</option>)}</select></label><p className="ap-weight-legend"><span aria-hidden="true" />Influence: blue = 0 · red = 1</p></>}{!playback.rig.hasWeights && <p>No skin weights in this preview.</p>}</div>}<canvas ref={canvas} className="ap-canvas" aria-label="Interactive 3D model preview" /><p>{stats}</p>{playback?.clips.length > 0 && <div className="ap-playback"><div className="ap-actions"><button onClick={() => viewer.current?.setPlaying(!playback.playing)}><Icon name={playback.playing ? 'pause' : 'run'} />{playback.playing ? 'Pause animation' : 'Play animation'}</button>{playback.clips.length > 1 && <label>Animation clip<select value={playback.clip} onChange={event => viewer.current?.setClip(+event.target.value)}>{playback.clips.map((name, index) => <option key={index} value={index}>{name}</option>)}</select></label>}</div><label className="ap-check"><input type="checkbox" checked={playback.follow} onChange={event => viewer.current?.setFollow(event.target.checked)} />Follow character</label><button onClick={() => viewer.current?.resetView()}><Icon name="preview" />Reset view</button><label>Animation time<input type="range" min="0" max={playback.duration} step="0.01" value={playback.time} onChange={event => viewer.current?.seek(+event.target.value)} /></label><span>{playback.time.toFixed(2)} / {playback.duration.toFixed(2)} s</span></div>}{quality && <details><summary>Remeshing quality</summary><p>{quality.output_triangles?.toLocaleString()} triangles · {quality.boundary_edges?.toLocaleString()} boundary edges{quality.quads_before_triangulation ? ` · ${quality.quads_before_triangulation.toLocaleString()} quads before triangulation` : ''}</p>{quality.boundary_edges > 0 && <p className="ap-warning">This mesh has open boundaries; it is not watertight.</p>}<pre>{JSON.stringify(quality, null, 2)}</pre></details>}{error && <p role="alert">{error}</p>}</div>
  }

  function Asset({ resource, value, load = false, onPoint, points = [], pointMode = 1, retire = false }) {
    const [blob, setBlob] = useState(null), [url, setUrl] = useState(''), [error, setError] = useState(''), [loading, setLoading] = useState(false)
    const [dimensions, setDimensions] = useState(null)
    const controller = useRef(null)
    async function fetchAsset() {
      controller.current?.abort()
      const c = new AbortController(); controller.current = c
      setLoading(true); setError('')
      try {
        const data = await asset(resource, value.artifact, c.signal)
        if (!c.signal.aborted) setBlob(data)
      } catch (error) { if (!c.signal.aborted) setError(error.message) }
      finally { if (!c.signal.aborted) setLoading(false) }
    }
    async function downloadAsset() {
      setLoading(true); setError('')
      try {
        const data = await asset(resource, value.artifact, sdk.signal)
        const href = URL.createObjectURL(data)
        const link = document.createElement('a')
        link.href = href; link.download = ['mesh', 'motion'].includes(value.type) ? 'asset.glb' : 'asset.png'
        link.click()
        setTimeout(() => URL.revokeObjectURL(href), 1000)
      } catch (error) { setError(error.message) }
      finally { setLoading(false) }
    }
    useEffect(() => { if (load) fetchAsset(); return () => controller.current?.abort() }, [resource, value.artifact, load])
    useEffect(() => { if (retire) { controller.current?.abort(); setBlob(null); setLoading(false) } }, [retire])
    useEffect(() => {
      if (!blob) { setUrl(''); return }
      const url = URL.createObjectURL(blob); setUrl(url)
      return () => URL.revokeObjectURL(url)
    }, [blob])
    return <div className="ap-asset">
      {!blob && ['mesh', 'motion'].includes(value.type) && !loading && !error && <p className="ap-help">3D preview is unloaded to save graphics memory. You can still download the asset.</p>}
      {!blob && <button onClick={fetchAsset} disabled={loading}><Icon name={error ? 'retry' : 'preview'} />{loading ? 'Loading preview…' : error ? 'Retry preview' : ['mesh', 'motion'].includes(value.type) ? 'Load 3D preview' : 'Load image'}</button>}
      {!blob && <button onClick={downloadAsset} disabled={loading}><Icon name="download" />Download asset</button>}
      {blob && ['mesh', 'motion'].includes(value.type) && <Mesh blob={blob} />}
      {blob && value.type === 'image' && <div className={`ap-image-points ${onPoint ? 'ap-image-pickable' : ''}`}><img src={url} alt={onPoint ? 'Click the object to include it; Shift-click background to exclude it' : 'Pipeline image output'} onLoad={event => setDimensions({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })} onClick={onPoint ? event => { const box = event.currentTarget.getBoundingClientRect(); onPoint([Math.min(event.currentTarget.naturalWidth - 1, Math.round((event.clientX-box.left)/box.width*event.currentTarget.naturalWidth)), Math.min(event.currentTarget.naturalHeight - 1, Math.round((event.clientY-box.top)/box.height*event.currentTarget.naturalHeight)), event.shiftKey ? 0 : pointMode]) } : undefined} />{dimensions && points.filter((_, i) => i % 3 === 0).map((_, i) => <span key={i} className={`ap-point ${points[i*3+2] ? 'ap-point-include' : 'ap-point-exclude'}`} style={{ left: `${points[i*3]/dimensions.width*100}%`, top: `${points[i*3+1]/dimensions.height*100}%` }} aria-label={`${points[i*3+2] ? 'Include' : 'Exclude'} point ${i+1}`}>{points[i*3+2] ? '+' : '−'}</span>)}</div>}
      {blob && <div className="ap-actions"><a href={url} download={['mesh', 'motion'].includes(value.type) ? 'asset.glb' : 'asset.png'}><Icon name="download" />Download asset</a><button onClick={() => { controller.current?.abort(); setBlob(null) }}><Icon name="close" />Unload preview</button></div>}
      {error && <div role="alert"><p>Could not load this asset. Retry the preview or download; you do not need to regenerate it.</p><details><summary>Technical details</summary>{error}</details></div>}
    </div>
  }

  function Page() {
    const [animationEnabled, setAnimationEnabled] = useState(readAnimationPreference)
    function toggleAnimation(enabled) {
      writeAnimationPreference(enabled)
      setAnimationEnabled(enabled)
    }
    const [resources, setResources] = useState([]), [resource, setResource] = useState(''), [view, setView] = useState(null)
    const [draft, setDraft] = useState(null), [checkpoint, setCheckpoint] = useState(null), [history, setHistory] = useState(false), [pageBefore, setPageBefore] = useState(0)
    const [error, setError] = useState(''), [working, setWorking] = useState(false), [name, setName] = useState('New asset')
    const [editText, setEditText] = useState({}), [picker, setPicker] = useState(null), [revising, setRevising] = useState({})
    const [creating, setCreating] = useState(false)
    const [pointModes, setPointModes] = useState({})
    const [saving, setSaving] = useState(false), [saveFailure, setSaveFailure] = useState('')
    const draftBase = useRef(null), actionBusy = useRef(false), failedSave = useRef('')
    const [leaving, setLeaving] = useState(null), leaveDialog = useRef(null)
    const [jumpAttempt, setJumpAttempt] = useState('')
    const current = useRef(null), turn = useRef(0), pending = useRef(null)
    const installed = sdk.hooks.useModels()
    current.current = { resource, view, draft, checkpoint, pageBefore }

    async function navigate(run) {
      if (actionBusy.current) return
      if (pending.current) { setError('Reconcile the last action before leaving this execution.'); return }
      if (current.current.draft) { setLeaving({ run }); return }
      actionBusy.current = true
      setWorking(true)
      try { await run() }
      catch (error) { setError(error.message) }
      finally { actionBusy.current = false; setWorking(false) }
    }
    function selectResource(id) {
      navigate(() => { ++turn.current; setView(null); setDraft(null); setCheckpoint(null); setPageBefore(0); setError(''); setResource(id); setEditText({}); setRevising({}) })
    }
    async function list() {
      try {
        const out = await sdk.resources.list()
        if (sdk.signal.aborted) return
        const available = out.resources || []
        setResources(available)
        if (!current.current.resource) {
          const selected = restoredSelection(available)
          if (selected) selectResource(selected)
        }
      } catch (error) { setError(error.message) }
    }
    async function read(id, branch = '', before = 0) {
      const seq = ++turn.current
      const out = unpack(await sdk.api.read({ resourceId: id, arguments: { tool: 'pipeline-inspect', branch, before } }))
      if (seq === turn.current && !sdk.signal.aborted) setView(out.view)
      return out.view
    }
    useEffect(() => { list() }, [])
    useEffect(() => { if (resource) rememberSelection(resource) }, [resource])
    useEffect(() => {
      setView(null); setDraft(null); setCheckpoint(null); setError('')
      if (!resource) return
      let alive = true, timer, initial = true
      async function poll() {
        const branch = initial ? '' : current.current.view?.branch || ''
        initial = false
        try { if (!actionBusy.current) await read(resource, branch, current.current.pageBefore) } catch (error) { if (alive) setError(error.message) }
        if (alive) timer = setTimeout(poll, 1000)
      }
      poll()
      return () => { alive = false; ++turn.current; clearTimeout(timer) }
    }, [resource])
    useEffect(() => { sdk.markDirty(!!draft); return () => sdk.markDirty(false) }, [draft])
    useEffect(() => { if (leaving) leaveDialog.current?.showModal() }, [leaving])

    const snapshot = checkpoint?.snapshot || view?.current
    const graph = draft || snapshot?.graph
    const nodes = snapshot?.nodes || {}
    const checkpointIndex = checkpoint ? view?.events.findIndex(event => event.id === checkpoint.id) : -1
    const events = checkpointIndex >= 0 ? view.events.slice(0, checkpointIndex + 1) : view?.events || []
    const running = Object.values(view?.current?.nodes || {}).some(n => n.status === 'running')
    const { active, dirty: dirtyNodes } = executionPreview(snapshot, draft)
    const issues = Object.fromEntries((graph?.nodes || []).map(n => [n.id, readiness(n, installed.models, n.input.value?.artifact || nodes[n.input.from]?.selected?.artifact)]))
    const waitingStage = graph?.nodes.find(n => active.has(n.id) && !dirtyNodes.has(n.id) && nodes[n.id]?.status === 'waiting')
    const decision = waitingStage ? ({ review: 'Review the suggested description', image: 'Choose an image', extract: 'Review the extracted image' })[waitingStage.kind] || `Review ${title(waitingStage).toLowerCase()} output` : ''
    const newestMesh = !checkpoint && view?.branch === view?.branches.at(-1)?.id ? Object.values(view?.current?.nodes || {}).map(n => n.attempt).filter(a => a?.outputs?.some(v => ['mesh', 'motion'].includes(v.type))).sort((a,b) => String(b.finished).localeCompare(String(a.finished)))[0]?.id : ''
    const validation = graphProblem(graph) || (!draft ? view?.validation : '')
    const planned = workbookPlan(snapshot, draft, true)
    const continuing = workbookPlan(snapshot, draft, false)
    const nextNode = continuing[0] || ''
    const nextStage = graph?.nodes.find(n => n.id === nextNode)
    const allIssue = animationIssue(graph, animationEnabled) || runAllIssue(snapshot, draft, installed.models)
    const runReady = !working && !running && !pending.current && !allIssue
    const continueIssue = !snapshot?.runAll ? 'Start a Run all execution first.' : !nextNode ? 'Choose the pending output before continuing.' : executionIssue(snapshot, draft, nextNode, installed.models)
    useEffect(() => {
      if (!jumpAttempt) return
      const output = document.getElementById(`output-${jumpAttempt}`)
      if (output) { output.scrollIntoView({ block: 'start', behavior: 'instant' }); setJumpAttempt('') }
    }, [jumpAttempt, view, checkpoint])

    async function jumpToDecision() {
      if (pageBefore) { setPageBefore(0); await read(resource, view.branch) }
      setJumpAttempt(nodes[waitingStage?.id]?.attempt?.id || '')
    }

    function edit(id, patch) {
      const next = copy(graph)
      const node = next.nodes.find(n => n.id === id); Object.assign(node, patch)
      if (node.kind === 'extract' && patch.input) node.params = { ...node.params, points: '[]' }
      if (node.kind === 'mesh' && patch.model !== undefined) {
        const model = installed.models.find(m => m.id === patch.model)
        const operation = model?.three_d_operations?.find(o => o.id === 'generate')
        next.target = { model: patch.model, version: '1', inputs: operation?.inputs?.map(i => i.type) || ['image'], advice: /trellis/i.test(patch.model) ? 'A single complete game asset centered on a plain black background, with no text, scenery or cropped parts.' : 'A single complete object, centered and clearly separated from its background.' }
      }
      changeDraft(next)
    }

    function savedGraph(s) { return s.checkpoint?.snapshot.graph || s.view.current.graph }

    function changeDraft(next) {
      const base = draftBase.current || current.current
      const value = same(next, savedGraph(base)) ? null : next
      if (!current.current.draft) draftBase.current = { ...current.current }
      current.current = { ...current.current, draft: value }
      setDraft(value); setSaveFailure('')
      if (!value) draftBase.current = null
    }

    // A saved card acknowledges only its own edits; other cards stay drafts.
    function adopt(next, cmd, state) {
      const base = cmd.action === 'configure' ? cmd.graph : savedGraph(draftBase.current || state)
      const remaining = rebaseEdits(base, current.current.draft, next.current.graph)
      const s = { ...current.current, view: next, draft: remaining, checkpoint: null }
      current.current = s
      draftBase.current = remaining ? { ...s, draft: null } : null
      setView(next); setDraft(remaining); setCheckpoint(null)
      return s
    }
    async function apply(cmd, state) {
      const s = state || current.current
      const body = { tool: 'pipeline-change', arguments: { command: cmd }, revision: { schema: 1, data: s.view.revision } }
      const op = operation()
      pending.current = { resource: s.resource, body, op, state: s }
      let out
      try { out = unpack(await sdk.resources.apply(s.resource, body, { operationId: op })) }
      catch (error) { if (!ambiguousFailure(error)) pending.current = null; throw error }
      pending.current = null
      const next = out.view || await read(s.resource, cmd.branch)
      return adopt(next, cmd, s)
    }
    async function reconcile() {
      const p = pending.current
      if (!p || actionBusy.current) return
      actionBusy.current = true; setWorking(true); ++turn.current
      try {
        const out = unpack(await sdk.resources.apply(p.resource, p.body, { operationId: p.op }))
        pending.current = null
        const next = out.view || await read(p.resource)
        adopt(next, p.body.arguments.command, p.state)
        setSaveFailure(''); setError('')
      } catch (error) {
        if (!ambiguousFailure(error)) pending.current = null
        if (failedSave.current) setSaveFailure(error.message)
        else setError(error.message)
      } finally { actionBusy.current = false; setWorking(false) }
    }
    async function saveNode(id, separate = false) {
      if (actionBusy.current || !current.current.draft) return false
      if (pending.current) { setSaveFailure('Reconcile the last action before saving.'); return false }
      actionBusy.current = true; setWorking(true); setSaving(true); ++turn.current
      failedSave.current = id
      try {
        let s = draftBase.current || current.current
        let submitted = graphForNode(savedGraph(s), current.current.draft, id)
        if (separate) {
          const latest = await read(resource, current.current.view.branch)
          submitted = graphForNode(latest.current.graph, current.current.draft, id)
          s = { ...current.current, view: latest, checkpoint: latest.events.at(-1) }
        }
        if (s.checkpoint) {
          s = await apply({ action: 'fork', branch: s.view.branch, checkpoint: s.checkpoint.id }, s)
        }
        await apply({ action: 'configure', branch: s.view.branch, graph: submitted }, s)
        setPageBefore(0); setSaveFailure(''); setError(''); failedSave.current = ''
        return true
      } catch (error) { setSaveFailure(error.message); return false }
      finally { actionBusy.current = false; setWorking(false); setSaving(false) }
    }
    async function mutate(cmd) {
      if (actionBusy.current) return false
      if (pending.current) { setError('Reconcile the last action before continuing.'); return false }
      const s0 = current.current
      const snap = s0.checkpoint?.snapshot || s0.view.current
      let problem = ''
      if (cmd.action === 'run-node') problem = animationIssue(s0.draft || snap.graph, animationEnabled, cmd.node) || executionIssue(snap, s0.draft, cmd.node, installed.models)
      if (cmd.action === 'run-all' || cmd.action === 'continue') problem = animationIssue(s0.draft || snap.graph, animationEnabled) || runAllIssue(snap, s0.draft, installed.models)
      if (cmd.action === 'select' && s0.draft) problem = 'Save or discard configuration changes before selecting an output.'
      if (problem) { setError(problem); return false }
      actionBusy.current = true
      setWorking(true); setError(''); ++turn.current
      setPageBefore(0)
      try {
        // Historical execution forks, but never saves unrelated draft controls.
        let s = current.current
        if (s.checkpoint) s = await apply({ action: 'fork', branch: s.view.branch, checkpoint: s.checkpoint.id }, s)
        await apply({ ...cmd, branch: s.view.branch }, s)
        return true
      } catch (error) { setError(error.message); return false }
      finally { actionBusy.current = false; setWorking(false) }
    }
    async function create() {
      return navigate(createFresh)
    }
    async function createFresh() {
      setWorking(true); setError('')
      try { const out = await sdk.resources.create({ arguments: { name: name.trim() } }); await list(); ++turn.current; setView(null); setDraft(null); setCheckpoint(null); setPageBefore(0); setResource(out.id); setCreating(false); setName('New asset') } catch (error) { setError(error.message) } finally { setWorking(false) }
    }
    async function upload(file, nodeId, slot = '', reportError = false) {
      if (!file || actionBusy.current || !view) return
      actionBusy.current = true
      setWorking(true); setError('')
      try {
        if (pending.current) throw new Error('Reconcile the last action before uploading.')
        const node = graph.nodes.find(n => n.id === nodeId)
        const type = slot ? node.inputs[slot].value.type : node.input.value.type
        const glb = ['mesh', 'motion'].includes(type)
        if (!file.size || file.size > (glb ? 128 : 20) * 1024 * 1024) throw new Error(glb ? 'GLB files must be nonempty and at most 128 MiB.' : 'Images must be nonempty and at most 20 MiB.')
        const upload = crypto.randomUUID()
        let out
        for (let offset = 0; offset < file.size; offset += 384 * 1024) {
          const chunk = file.slice(offset, offset + 384 * 1024)
          const data = await new Promise((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result).split(',')[1]); reader.onerror = reject; reader.readAsDataURL(chunk) })
          out = unpack(await sdk.resources.apply(resource, { tool: 'pipeline-upload', arguments: { name: file.name || (glb ? 'asset.glb' : 'image.png'), mime: glb ? 'model/gltf-binary' : file.type, data, upload, offset, final: offset + chunk.size === file.size }, revision: { schema: 1, data: current.current.view.revision } }))
        }
        if (!out?.id) throw new Error('The upload did not produce an asset')
        const next = copy(graph), source = next.nodes.find(n => n.id === nodeId)
        if (slot) source.inputs[slot] = { value: { type, artifact: out.id } }
        else source.input = { value: { type, artifact: out.id } }
        if (source.kind === 'extract') source.params = { ...source.params, points: '[]' }
        changeDraft(next)
      } catch (error) { setError(error.message); if (reportError) throw error } finally { actionBusy.current = false; setWorking(false) }
    }

    return <section className="ap-app" aria-label="3D asset pipeline">
      {picker && <ImagePicker onClose={() => setPicker(null)} onSelect={file => upload(file, picker.node, picker.slot, true)} />}
      <header id="pipeline-controls" className="ap-header"><div className="ap-toolbar"><h1 className="page-title"><Icon name="mesh" />3D Asset Pipeline</h1>{view && <div className="ap-run-actions"><span data-testid="configuration-save-state" role="status">{saveFailure ? 'Changes not saved' : saving ? 'Saving changes…' : draft ? 'Unsaved changes' : 'All changes saved'}</span><button className="ap-run" title={allIssue || 'Restart every active stage in a new execution'} onClick={() => mutate({ action: 'run-all' })} disabled={!runReady}><Icon name="run" />Run all</button>{snapshot?.runAll && <button className="ap-run" title={continueIssue || 'Resume this execution without restarting completed stages'} onClick={() => mutate({ action: 'continue' })} disabled={working || running || !!allIssue || !!continueIssue || !!pending.current}><Icon name="step" />Continue</button>}{running && <button onClick={() => mutate({ action: 'cancel' })} disabled={working}><Icon name="close" />Cancel</button>}</div>}</div><div className="ap-library">
        <div className="ap-animation-toggle" title={draft ? 'Save or discard changes before toggling animation.' : undefined}>
          <Switch label={ANIMATION_LABEL} checked={animationEnabled} onChange={toggleAnimation} disabled={running || working || saving || !!draft} />
          <span>{ANIMATION_LABEL}</span>
        </div>
        <select aria-label="Saved pipeline" value={resource} disabled={working} onChange={e => selectResource(e.target.value)}><option value="">Select a pipeline</option>{resources.map(r => <option key={r.id} value={r.id}>{r.name}</option>)}</select>
        <details className="ap-new" open={creating} onToggle={event => setCreating(event.currentTarget.open)}><summary>Create a pipeline</summary><div className="ap-actions"><label>Pipeline name<input aria-label="New pipeline name" value={name} onChange={e => setName(e.target.value)} /></label><button onClick={create} disabled={working || !name.trim()}><Icon name="add" />New pipeline</button></div></details>
      </div>{view && <div className="ap-readiness" role="status">{running ? 'Processing…' : decision ? `${decision} to continue.` : nextStage ? `Next: ${title(nextStage)}` : 'Save configuration or run a processing stage.'}{allIssue && <span>{allIssue}</span>}{decision && <button onClick={jumpToDecision}><Icon name="down" />Go to pending output</button>}<a className="ap-status-jump" href="#pipeline-status">View status and outputs ↓</a></div>}{planned.length > 0 && <p className="ap-run-plan">Run all restarts: {planned.map(id => title(graph.nodes.find(n => n.id === id))).join(' → ')}{graph.nodes.find(n => n.id === planned.at(-1))?.pause ? ' · then pause for review' : ''}</p>}{snapshot?.runAll && continuing.length > 0 && <p className="ap-run-plan">Continue: {continuing.map(id => title(graph.nodes.find(n => n.id === id))).join(' → ')}</p>}</header>
      {checkpoint && <p className="ap-warning" role="status">Viewing historical checkpoint · {new Date(checkpoint.at).toLocaleString()}. Changes will create a new execution branch.</p>}
      {leaving && <dialog ref={leaveDialog} className="ap-leave" aria-labelledby="pipeline-leave-title" onCancel={() => setLeaving(null)}><h2 id="pipeline-leave-title">Unsaved configuration changes</h2><p>Save each edited card before leaving, or discard these edits. Saved checkpoints and results will be kept.</p><div className="ap-actions"><button autoFocus onClick={() => setLeaving(null)}>Keep editing</button><button onClick={() => { const action = leaving; current.current = { ...current.current, draft: null }; draftBase.current = null; setDraft(null); setSaveFailure(''); setLeaving(null); navigate(action.run) }}>Discard changes</button></div></dialog>}
      {error && <div role="alert" className="ap-error">{error}{pending.current && !saveFailure && <button disabled={working} onClick={reconcile}><Icon name="retry" />Reconcile last action</button>}</div>}
      {saveFailure && <div role="alert" className="ap-error">Configuration could not be saved: {saveFailure} Your edits are retained.{pending.current ? <button disabled={saving || working} onClick={reconcile}><Icon name="retry" />Reconcile last action</button> : /revision.*conflict/i.test(saveFailure) ? <><p>The saved execution changed in another session. Save your retained configuration in a separate branch to preserve both versions.</p><button disabled={saving || working} onClick={() => saveNode(failedSave.current, true)}><Icon name="save" />Save edits in new branch</button></> : <button disabled={saving || working} onClick={() => saveNode(failedSave.current)}><Icon name="save" />Retry saving</button>}</div>}
      {!view && <p role="status">{resource ? 'Loading pipeline…' : 'Create a pipeline to begin.'}</p>}
      {view && graph && <div className="ap-split"><aside className="ap-controls">
        <div className="ap-tabs"><button aria-pressed={!history} onClick={() => setHistory(false)}><Icon name="nodes" />Nodes</button><button aria-pressed={history} onClick={() => setHistory(true)}><Icon name="history" />Executions</button></div>
        {history ? <ul>{view.branches.map(b => <li key={b.id}><button disabled={working} onClick={() => navigate(async () => { setDraft(null); setCheckpoint(null); setPageBefore(0); try { const next = await read(resource, b.id); if (b.id !== next.branches.at(-1)?.id) setCheckpoint(next.events.at(-1)) } catch (error) { setError(error.message) } })}>{new Date(b.created).toLocaleString()} · {b.state}{b.parent ? ' · fork' : ''}</button></li>)}</ul> : <ChainConnectors key={String(animationEnabled)} graph={graph}>{graph.nodes.filter(node => animationEnabled || !isAnimationNode(node)).map(node => {
          const state = nodes[node.id] || {}, dirty = dirtyNodes.has(node.id)
          const changed = nodeChanged(snapshot.graph, draft, node.id)
          const runIssue = animationIssue(graph, animationEnabled, node.id) || executionIssue(snapshot, draft, node.id, installed.models)
          const rerun = state.attempt && !dirty && state.status !== 'stale'
          const segment = node.kind === 'extract' && node.params?.mode === 'segment'
          const extractionInput = node.input.value?.artifact ? node.input.value : nodes[node.input.from]?.selected
          const points = pointCoordinates(node.params?.points)
          const stalePoints = points?.length && node.params?.points_image && node.params.points_image !== extractionInput?.artifact
          const waiting = !dirty && state.status === 'waiting'
          return <article key={node.id} data-node={node.id} className={`ap-node ${!active.has(node.id) ? 'ap-inactive' : ''} ${snapshot.last === node.id && !dirty ? ['failed', 'interrupted'].includes(state.status) ? 'ap-failed' : state.status === 'succeeded' ? 'ap-last' : '' : ''} ${nextNode === node.id ? 'ap-next' : ''} ${waiting ? 'ap-waiting' : ''}`}>
            <div className="ap-node-heading"><h2><Icon name={node.kind} />{title(node)}</h2>{isProcessing(node) && <label className={`ap-primary ${graph.primary === node.id ? 'ap-primary-selected' : ''}`} title="Run this stage and only the earlier stages it needs"><input type="radio" name="primary" checked={graph.primary === node.id} onChange={() => changeDraft({ ...copy(graph), primary: node.id })} disabled={running || working} />Primary output</label>}</div>
            <p className="ap-help">{node.id === 'motion-description' ? 'Describe the movement, direction and pace for a short human-motion clip.' : node.params?.purpose === 'motion' ? 'Review the movement description before generating motion. You can bypass this stage by using the description directly.' : descriptions[node.kind]}</p>
            <div className="ap-badges">{!active.has(node.id) && <span>Inactive</span>}{changed && <span className="ap-unsaved">Unsaved changes</span>}{isProcessing(node) ? <span className={waiting ? 'ap-waiting-badge' : ''}>{!state.attempt ? 'Not run' : dirty ? 'stale' : waiting ? node.kind === 'image' ? 'Choose an image' : 'Review output' : state.status}</span> : !changed && <span>{node.input.value?.text?.trim() ? 'Saved input' : 'Input required'}</span>}{isProcessing(node) && snapshot.last === node.id && !dirty && <span>{waiting ? 'Waiting for your selection' : 'Last executed'}</span>}{nextNode === node.id && <span className="ap-next-badge">Next</span>}</div>
            {waiting && <button onClick={jumpToDecision}><Icon name="down" />{node.kind === 'image' ? 'Choose an image' : 'Review output'}</button>}
            <fieldset disabled={running || working}>
              {node.kind === 'extract' && <label>Extraction mode<select value={node.params?.mode || 'keep'} onChange={e => edit(node.id, { params: { ...node.params, mode: e.target.value } })}><option value="keep">Keep existing image / alpha</option><option value="segment">Segment object</option></select></label>}
              {needsModel(node) && <label>Model<select value={node.model || ''} onChange={e => edit(node.id, { model: e.target.value })}><option value="">Select model</option>{node.model && !installed.models.some(m => m.id === node.model && compatibleModel(m, node)) && <option value={node.model}>{node.model} (unavailable or incompatible)</option>}{installed.models.filter(m => compatibleModel(m, node)).map(m => <option key={m.id} value={m.id}>{m.id}</option>)}</select>{!installed.models.some(m => compatibleModel(m, node)) && <span className="ap-help">No compatible models installed for this stage.</span>}</label>}
              {node.kind !== 'source' && <label>Input<select value={node.input.from || 'direct'} onChange={e => edit(node.id, { input: e.target.value === 'direct' ? { value: { type: inputType(node), ...(inputType(node) === 'text' ? { text: '' } : {}) } } : { from: e.target.value } })}><option value="direct">{inputType(node) === 'image' ? 'Upload or choose an image' : inputType(node) === 'mesh' ? 'Upload a 3D asset' : 'Write a description'}</option>{inputChoices(graph, node).map(n => <option key={n.id} value={n.id}>Output of {title(n)}</option>)}</select></label>}
              {['skin', 'retarget'].includes(node.kind) && (() => {
                const name = node.kind === 'skin' ? 'skeleton' : 'motion'
                const binding = node.inputs?.[name] || {}
                return <label>{name === 'skeleton' ? 'Skeleton input' : 'Motion input'}<select value={binding.from || 'direct'} onChange={e => edit(node.id, { inputs: { ...node.inputs, [name]: e.target.value === 'direct' ? { value: { type: name === 'motion' ? 'motion' : 'mesh' } } : { from: e.target.value } } })}><option value="direct">Upload {name} GLB</option>{inputChoices(graph, node, name).map(n => <option key={n.id} value={n.id}>Output of {title(n)}</option>)}</select></label>
              })()}
              {node.input.value?.type === 'text' && <label>Description<textarea aria-label="Description" value={node.input.value.text || ''} onChange={e => edit(node.id, { input: { value: { type: 'text', text: e.target.value } } })} /></label>}
              {node.kind !== 'source' && node.input.value?.type === 'image' && <label>Upload image<input type="file" accept="image/png,image/jpeg" onChange={e => upload(e.target.files[0], node.id)} /></label>}
              {node.input.value?.type === 'mesh' && <label>Upload 3D asset<input type="file" accept=".glb,model/gltf-binary" onChange={e => upload(e.target.files[0], node.id)} /></label>}
              {Object.entries(node.inputs || {}).filter(([slot, binding]) => slot !== 'reference' && binding.value).map(([slot, binding]) => <div key={slot}>{slot === 'skeleton' && <label>Skeleton file type<select value={binding.value.type} onChange={e => edit(node.id, { inputs: { ...node.inputs, [slot]: { value: { type: e.target.value } } } })}><option value="mesh">Rigged mesh</option><option value="motion">Motion skeleton</option></select></label>}<label>Upload {slot} GLB<input type="file" accept=".glb,model/gltf-binary" onChange={e => upload(e.target.files[0], node.id, slot)} /></label>{binding.value.artifact && <Asset resource={resource} value={binding.value} />}</div>)}
              {node.input.value?.artifact && !segment && <Asset resource={resource} value={node.input.value} load={node.input.value.type === 'image'} />}
              {node.kind !== 'source' && node.input.value?.type === 'image' && <Button onClick={() => setPicker({ node: node.id, slot: '' })}>Choose from library</Button>}
              {node.kind === 'image' && <label>Reference image<select value={node.inputs?.reference?.from || (node.inputs?.reference ? 'direct' : 'none')} onChange={e => {
                const inputs = { ...node.inputs }
                if (e.target.value === 'none') delete inputs.reference
                else inputs.reference = e.target.value === 'direct' ? { value: { type: 'image' } } : { from: e.target.value }
                edit(node.id, { inputs })
              }}><option value="none">None — text only</option><option value="direct">Upload or choose an image</option>{inputChoices(graph, node, 'reference').map(n => <option key={n.id} value={n.id}>Output of {title(n)}</option>)}</select><span className="ap-help">Choose a model that supports reference images or image editing.</span></label>}
              {node.kind === 'image' && node.inputs?.reference?.value && <div><label>Upload reference image<input type="file" accept="image/png,image/jpeg" onChange={e => upload(e.target.files[0], node.id, 'reference')} /></label><Button onClick={() => setPicker({ node: node.id, slot: 'reference' })}>Choose reference from library</Button>{node.inputs.reference.value.artifact && <Asset resource={resource} value={node.inputs.reference.value} load />}</div>}
              {node.kind === 'image' && <label>Image count<select value={node.count} onChange={e => edit(node.id, { count: +e.target.value, pause: +e.target.value > 1 || node.pause })}>{[1,2,3,4].map(n => <option key={n}>{n}</option>)}</select></label>}
              {segment && <><label>Object description<input value={node.params?.prompt || ''} onChange={e => edit(node.id, { params: { ...node.params, prompt: e.target.value } })} /></label><p className="ap-help">Click the object to include it. Shift-click the background to exclude it, or select Exclude before tapping. Model support varies.</p><div className="ap-actions" role="group" aria-label="Segmentation points"><button type="button" aria-pressed={(pointModes[node.id] ?? 1) === 1} onClick={() => setPointModes({ ...pointModes, [node.id]: 1 })}>Include (+)</button><button type="button" aria-pressed={pointModes[node.id] === 0} onClick={() => setPointModes({ ...pointModes, [node.id]: 0 })}>Exclude (−)</button><button disabled={!points?.length} onClick={() => edit(node.id, { params: { ...node.params, points: JSON.stringify(points.slice(0,-3)) } })}>Undo point</button><button disabled={!points?.length} onClick={() => edit(node.id, { params: { ...node.params, points: '[]' } })}>Clear points</button></div><p className="ap-help">{(points?.length || 0)/3} points selected</p>{stalePoints ? <p role="alert">Input image changed. Clear the old segmentation points before continuing.</p> : null}{extractionInput ? <Asset key={extractionInput.artifact} resource={resource} value={extractionInput} load points={stalePoints ? [] : points || []} pointMode={pointModes[node.id] ?? 1} onPoint={!running && !working && !stalePoints ? point => edit(node.id, { params: { ...node.params, points_image: extractionInput.artifact, points: JSON.stringify([...(points || []), ...point]) } }) : undefined} /> : <p className="ap-help">An input image will appear here after upload or upstream selection.</p>}<details><summary>Point coordinates</summary><label>Points (x,y,label triples)<input value={node.params?.points || ''} onChange={e => edit(node.id, { params: { ...node.params, points_image: extractionInput?.artifact || '', points: e.target.value } })} /></label>{!points && <p role="alert">Use a JSON array of x, y, label triples; labels are 1 (include) or 0 (exclude).</p>}</details></>}
              {node.kind === 'mesh' && <label>Quality<select value={node.params?.quality || '512'} onChange={e => edit(node.id, { params: { ...node.params, quality: e.target.value } })}>{['coarse','512','1024','auto'].map(q => <option key={q}>{q}</option>)}</select></label>}
              {['remesh', 'rig', 'skin', 'motion'].includes(node.kind) && stageParameters(installed.models.find(m => m.id === node.model), node).map(p => <label key={p.name}>{p.label}{p.type === 'enum' ? <select value={node.params?.[p.name] ?? p.default ?? ''} onChange={e => edit(node.id, { params: { ...node.params, [p.name]: e.target.value } })}>{p.options.map(v => <option key={v}>{v}</option>)}</select> : <input type="number" min={p.min} max={p.max || undefined} step={p.type === 'integer' ? 1 : 'any'} value={node.params?.[p.name] ?? p.default ?? ''} onChange={e => { const params = { ...node.params }; if (e.target.value === '') delete params[p.name]; else params[p.name] = e.target.value; edit(node.id, { params }) }} />}</label>)}
              {['image','mesh','rig','skin','motion'].includes(node.kind) && <details><summary>Generation settings</summary><label>Seed<input type="number" value={node.seed} min="0" max="2147483647" onChange={e => edit(node.id, { seed: +e.target.value })} /></label></details>}
              {node.kind !== 'source' && <label className="ap-check"><input type="checkbox" checked={node.pause} disabled={node.kind === 'image' && node.count > 1} onChange={e => edit(node.id, { pause: e.target.checked })} />Pause for output review{node.kind === 'image' && node.count > 1 && <span className="ap-help">Multiple images pause for selection.</span>}</label>}
              {active.has(node.id) && issues[node.id].some(issue => !stalePoints || !issue.startsWith('Input image changed.')) && <ul className="ap-needs" aria-label={`${title(node)} needs configuration`}>{issues[node.id].filter(issue => !stalePoints || !issue.startsWith('Input image changed.')).map(issue => <li key={issue}>{issue}</li>)}</ul>}
              <div className="ap-node-actions"><button disabled={!changed || working || running || !!pending.current} onClick={() => saveNode(node.id)}><Icon name="save" />Save</button>{isProcessing(node) && <button className="ap-run" title={runIssue || (rerun ? 'Execute again and preserve the previous result in history' : 'Execute this stage using its saved configuration')} disabled={working || running || !!pending.current || !!runIssue} onClick={() => mutate({ action: 'run-node', node: node.id })}><Icon name={rerun ? 'retry' : 'run'} />{rerun ? 'Rerun' : 'Run'}</button>}</div>{isProcessing(node) && runIssue && active.has(node.id) && <p className="ap-help">{runIssue}</p>}
            </fieldset>
          </article>
        })}</ChainConnectors>}
      </aside><Timeline activity={running && <div role="status" className="ap-running" aria-live="polite">
        <span className="ap-spinner" aria-hidden="true" />
        <div><strong>{view.current.graph.nodes.filter(node => view.current.nodes[node.id]?.status === 'running').map(node => title(node)).join(', ')} · Running</strong><span>Inference or processing is active. You can leave this page and return later.</span></div>
      </div>}>
        <div className="ap-timeline-heading"><h2><Icon name="status" />Status and outputs</h2><a href="#pipeline-controls">Back to nodes ↑</a></div>
        {decision && !checkpoint && <div className="ap-decision" role="status"><strong>{decision} to continue.</strong><button onClick={jumpToDecision}><Icon name="down" />Go to pending output</button></div>}
        {checkpoint && <div className="ap-warning">Inspecting checkpoint from {new Date(checkpoint.at).toLocaleString()}. Editing or executing will fork it.<button onClick={() => navigate(() => { setCheckpoint(null); setDraft(null); setPageBefore(0); read(resource).catch(error => setError(error.message)) })}><Icon name="history" />Return to latest state</button></div>}
        {validation && <p role="alert" className="ap-error">{validation}</p>}
        {draft && <p className="ap-warning">{saveFailure ? 'Save failed; edits remain in this tab.' : 'Unsaved configuration changes. Use Save on each edited card to record a checkpoint.'}</p>}
        {pageBefore > 0 && <button onClick={() => { setPageBefore(0); read(resource, view.branch).catch(e => setError(e.message)) }}><Icon name="history" />Latest events</button>}
        {view.before > 0 && <button onClick={() => { setPageBefore(view.before); read(resource, view.branch, view.before).catch(e => setError(e.message)) }}><Icon name="history" />Earlier events</button>}
        {events.map(event => {
          const state = event.snapshot.nodes[event.node], attempt = state?.attempt
          const eventNode = attempt?.arguments.node || event.snapshot.graph.nodes.find(n => n.id === event.node)
          const stageName = eventNode ? title(eventNode) : event.node
          const live = attempt && !dirtyNodes.has(event.node) && nodes[event.node]?.attempt?.id === attempt.id
          const accepted = live && nodes[event.node]?.status === 'succeeded'
          const maySelect = live && attempt.arguments.node.kind !== 'source' && (nodes[event.node]?.status === 'waiting' || (accepted && revising[attempt.id]))
          if (event.kind === 'progress') return <details key={event.id} className="ap-progress"><summary>{stageName}: {event.message.slice(0, 100)}</summary><pre>{attempt?.progress || event.message}</pre><button onClick={() => navigate(() => { setCheckpoint(event); setDraft(null); setHistory(false) })}><Icon name="rewind" />Rewind</button></details>
          return <article key={event.id} id={event.kind === 'completed' ? `output-${attempt?.id}` : undefined} className={`ap-event ap-event-${event.kind}`}><header><strong>{event.node ? stageName : 'Pipeline'}</strong><time>{new Date(event.at).toLocaleTimeString()}</time><button onClick={() => navigate(() => { setCheckpoint(event); setDraft(null); setHistory(false) })}><Icon name="rewind" />Rewind</button></header><p>{event.kind === 'completed' && accepted ? 'Output accepted' : event.message}</p>
            {event.kind === 'started' && attempt && <details open><summary>Inputs · {attempt.arguments.node.model || 'No model'}</summary>{attempt.arguments.input.type === 'text' ? <pre>{attempt.arguments.input.text}</pre> : <Asset resource={resource} value={attempt.arguments.input} />}{Object.entries(attempt.arguments.inputs || {}).map(([name, value]) => <div key={name}><strong>{name === 'motion' ? 'Motion input' : name === 'skeleton' ? 'Skeleton input' : name}</strong><Asset resource={resource} value={value} /></div>)}<details><summary>Resolved parameters</summary><pre>{JSON.stringify(attempt.arguments, null, 2)}</pre></details></details>}
            {['completed', 'source'].includes(event.kind) && attempt && <><p>{attempt.explanation}</p><div className={attempt.outputs?.length > 1 ? 'ap-candidates' : ''}>{attempt.outputs?.map((value, index) => <div key={index} className={`ap-output ${accepted && nodes[event.node]?.selected?.artifact === value.artifact && value.artifact ? 'ap-output-selected' : ''}`}>
              {value.type === 'image' && <h3 className="ap-candidate-title">Image {index+1}</h3>}
              {value.type === 'text' ? maySelect ? <label>Suggested description<textarea value={editText[event.id] ?? nodes[event.node]?.selected?.text ?? value.text} onChange={e => setEditText({ ...editText, [event.id]: e.target.value })} /></label> : <div><strong>{attempt.arguments.node.kind === 'source' ? 'Description' : accepted ? 'Accepted description' : 'Suggested description'}</strong><p className="ap-output-text">{accepted ? nodes[event.node]?.selected?.text || value.text : value.text}</p></div> : <Asset resource={resource} value={value} load={value.type === 'image' || (['mesh', 'motion'].includes(value.type) && attempt.id === newestMesh)} retire={['mesh', 'motion'].includes(value.type) && attempt.id !== newestMesh} />}
              {accepted && value.artifact && nodes[event.node]?.selected?.artifact === value.artifact && <p className="ap-selection-label">Selected output</p>}
              {maySelect && <button disabled={working || running || !!draft} onClick={async () => { await mutate({ action: 'select', node: event.node, index, ...(value.type === 'text' ? { text: editText[event.id] ?? nodes[event.node]?.selected?.text ?? value.text } : {}) }); setRevising({ ...revising, [attempt.id]: false }) }}><Icon name="check" />{value.type === 'text' ? 'Accept text' : 'Choose this output'}</button>}
            </div>)}</div>{attempt.arguments.node.kind === 'review' && maySelect && <button disabled={working || running || !!draft} onClick={async () => { await mutate({ action: 'select', node: event.node, index: 0, text: attempt.arguments.input.text }); setRevising({ ...revising, [attempt.id]: false }) }}><Icon name="check" />Keep original description</button>}{accepted && attempt.arguments.node.kind !== 'source' && !revising[attempt.id] && <button disabled={working || running || !!draft} onClick={() => setRevising({ ...revising, [attempt.id]: true })}><Icon name="edit" />Change selection</button>}<details><summary>Execution details and logs</summary><pre>{JSON.stringify({ attempt: attempt.id, started: attempt.started, finished: attempt.finished, error: attempt.error, diagnostics: attempt.diagnostics }, null, 2)}</pre>{sdk.viewer?.isAdmin?.() && <a href={`/app/traces?tab=backend&q=${encodeURIComponent(attempt.arguments.node.model || '')}`} onClick={e => { if (sdk.navigation?.openApp) { e.preventDefault(); sdk.navigation.openApp(`/app/traces?tab=backend&q=${encodeURIComponent(attempt.arguments.node.model || '')}`) } }}>Search API / backend traces for this model</a>}</details></>}
          </article>
        })}
      </Timeline></div>}
    </section>
  }
  return { Component: Page, dispose() {} }
}
