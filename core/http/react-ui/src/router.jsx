import { lazy } from 'react'
import { createBrowserRouter, Navigate, useLocation, useParams } from 'react-router-dom'
import { routerBasename } from './utils/basePath'
import App from './App'
import RequireAdmin from './components/RequireAdmin'
import RequireAuth from './components/RequireAuth'
import RequireAuthEnabled from './components/RequireAuthEnabled'
import RequireFeature from './components/RequireFeature'
import FeatureOff from './components/identity/FeatureOff'
import ToolOff from './components/tools/ToolOff'

// Pages are code-split: each becomes its own chunk loaded on demand, so a route
// no longer drags every other page (and its heavy deps — CodeMirror, the MCP
// SDK, yaml, marked) into the initial bundle. The <Suspense> boundary in
// App.jsx (around <Outlet/>) shows nothing while a chunk loads, keeping the
// sidebar/header mounted.
//
// `page(key, loader)` registers the dynamic import under a route-segment key
// (the first segment after /app/) so a NavLink can warm the chunk on hover via
// `preloadRoute('/app/chat')`. Dynamic import() is memoised by the module
// loader, so a preloaded chunk is reused — not re-fetched — when the user
// actually navigates. Pages with `key: null` aren't sidebar-reachable; they
// still code-split, they just won't be preloaded from the nav.
const preloaders = {}

// A deploy swaps the whole content-hashed asset set at once, so a tab holding
// an older index.html (or one whose request lands on a replica that hasn't
// been swapped yet, the normal state during a rolling update) asks for a page
// chunk the server no longer has. The import rejects and React Router's default
// error boundary replaces the app with "Unexpected Application Error!" until
// someone thinks to reload. Reloading is what fixes it, so do it automatically:
// index.html is served no-cache, so the reload lands on a self-consistent set.
//
// The timestamp guard bounds that to one reload per RELOAD_WINDOW_MS. Without
// it, a chunk that is genuinely gone rather than merely stale would reload the
// app forever, which is worse than the error screen: it never settles and never
// says why. The failure isn't narrowed to fetch errors: the message differs per
// browser and a missed match costs the recovery, while a module that throws
// while evaluating costs one wasted reload before the error surfaces anyway.
const RELOAD_KEY = 'localai.chunkReloadedAt'
const RELOAD_WINDOW_MS = 10_000
let reloading = false

function claimReload() {
  try {
    const last = Number(window.sessionStorage.getItem(RELOAD_KEY)) || 0
    if (Date.now() - last < RELOAD_WINDOW_MS) return false
    window.sessionStorage.setItem(RELOAD_KEY, String(Date.now()))
    return true
  } catch {
    // No usable sessionStorage means no way to bound the reloads.
    return false
  }
}

function page(key, loader) {
  if (key !== null) preloaders[key] = loader
  // preloadRoute keeps the raw loader: a hover that fails must stay silent
  // rather than reload the page out from under the pointer. The click that
  // follows goes through this wrapper and recovers there.
  return lazy(() => loader().catch(err => {
    // A reload is already committing: a second chunk failing in the same
    // document must wait for it, not throw and flash the error boundary.
    if (reloading) return new Promise(() => {})
    if (!claimReload()) throw err
    reloading = true
    window.location.reload()
    // Stay pending. Resolving or rejecting here would flash the error boundary
    // in the frames before the reload commits.
    return new Promise(() => {})
  }))
}

export function preloadRoute(path) {
  if (!path) return
  const m = path.match(/^\/app(?:\/([^/?#]*))?/)
  if (!m) return
  preloaders[m[1] ?? '']?.().catch(() => { /* network blip — real click will retry */ })
}

const Home = page('', () => import('./pages/Home'))
const GroupChat = page(null, () => import('./pages/GroupChat'))
const Chat = page('chat', () => import('./pages/Chat'))
const Models = page('models', () => import('./pages/Models'))
const ManageRedirect = page('manage', () => import('./pages/ManageRedirect'))
const ImageGen = page('image', () => import('./pages/ImageGen'))
const VideoGen = page('video', () => import('./pages/VideoGen'))
const ThreeDGen = page('3d', () => import('./pages/ThreeDGen'))
const TTS = page('tts', () => import('./pages/TTS'))
const Sound = page('sound', () => import('./pages/Sound'))
const Diarization = page('diarization', () => import('./pages/Diarization'))
const AudioTransform = page('transform', () => import('./pages/AudioTransform'))
const Talk = page('talk', () => import('./pages/Talk'))
// Referenced only from JSX below — same blind spot as Activity further down.
// eslint-disable-next-line no-unused-vars
const OperateOverview = page('operate', () => import('./pages/OperateOverview'))
const Backends = page('backends', () => import('./pages/Backends'))
// Only referenced from JSX below, which eslint cannot see without
// eslint-plugin-react. Suppressed here rather than left to widen the file's
// warning count; the surrounding page consts predate the lint baseline.
// eslint-disable-next-line no-unused-vars
const Activity = page('activity', () => import('./pages/Activity'))
const Settings = page('settings', () => import('./pages/Settings'))
const Traces = page('traces', () => import('./pages/Traces'))
const P2P = page('p2p', () => import('./pages/P2P'))
const Agents = page('agents', () => import('./pages/Agents'))
const AgentCreate = page(null, () => import('./pages/AgentCreate'))
// eslint-disable-next-line no-unused-vars
const AgentPage = page(null, () => import('./pages/AgentPage'))
// eslint-disable-next-line no-unused-vars
const AgentRun = page(null, () => import('./pages/AgentRun'))
const AgentStatus = page(null, () => import('./pages/AgentStatus'))
const Collections = page('collections', () => import('./pages/Collections'))
const Skills = page('skills', () => import('./pages/Skills'))
const SkillEdit = page(null, () => import('./pages/SkillEdit'))
const AgentJobs = page('agent-jobs', () => import('./pages/AgentJobs'))
const AgentTaskDetails = page(null, () => import('./pages/AgentTaskDetails'))
const AgentJobDetails = page(null, () => import('./pages/AgentJobDetails'))
const ModelEditor = page(null, () => import('./pages/ModelEditor'))
// Rendered by Models, through its <Outlet/>: the list stays mounted behind it.
// eslint-disable-next-line no-unused-vars
const ModelPage = page(null, () => import('./pages/ModelPage'))
// PipelineEditor removed — the Model Editor with templates handles all model types
const ImportModel = page(null, () => import('./pages/ImportModel'))
const BackendLogs = page(null, () => import('./pages/BackendLogs'))
const Explorer = page(null, () => import('./pages/Explorer'))
const Login = page(null, () => import('./pages/Login'))
const FineTune = page('fine-tune', () => import('./pages/FineTune'))
const Quantize = page('quantize', () => import('./pages/Quantize'))
const Studio = page('studio', () => import('./pages/Studio'))
const FaceRecognition = page('face', () => import('./pages/FaceRecognition'))
const VoiceRecognition = page('voice', () => import('./pages/VoiceRecognition'))
const VoiceProfileCreate = page(null, () => import('./pages/VoiceProfileCreate'))
const Nodes = page('nodes', () => import('./pages/Nodes'))
const Scheduling = page('scheduling', () => import('./pages/Scheduling'))
const NodeBackendLogs = page(null, () => import('./pages/NodeBackendLogs'))
const NodeDetail = page(null, () => import('./pages/NodeDetail'))
// eslint-disable-next-line no-unused-vars
const AddNode = page(null, () => import('./pages/AddNode'))
const NotFound = page(null, () => import('./pages/NotFound'))
const Usage = page('usage', () => import('./pages/Usage'))
// The Traffic hub's other pages. Only referenced from JSX below, which eslint
// cannot see.
// eslint-disable-next-line no-unused-vars
const TrafficOverview = page('traffic', () => import('./pages/TrafficOverview'))
// eslint-disable-next-line no-unused-vars
const TrafficModels = page(null, () => import('./pages/TrafficModels'))
// eslint-disable-next-line no-unused-vars
const TrafficHost = page(null, () => import('./pages/TrafficHost'))
// eslint-disable-next-line no-unused-vars
const Prometheus = page(null, () => import('./pages/Prometheus'))
// eslint-disable-next-line no-unused-vars
const TracePage = page(null, () => import('./pages/TracePage'))
const Users = page('users', () => import('./pages/Users'))
const Middleware = page('middleware', () => import('./pages/Middleware'))
const Failover = page('failover', () => import('./pages/Failover'))
const Account = page('account', () => import('./pages/Account'))
// Only referenced from JSX below, which eslint cannot see.
// eslint-disable-next-line no-unused-vars
const BuildOverview = page('build', () => import('./pages/BuildOverview'))

import HubLayout from './components/hub/HubLayout'
import { buildHub, operateHub } from './components/hub/hubConfig'

// The Agent chat page became the agent's own page and its runs. An old chat
// link opens the agent page and keeps the user in the query.
// eslint-disable-next-line no-unused-vars, react-refresh/only-export-components
function AgentChatRedirect() {
  const { name } = useParams()
  const { search } = useLocation()
  return <Navigate to={`/app/agents/${encodeURIComponent(name)}${search}`} replace />
}

function BrowseRedirect() {
  const { '*': splat } = useParams()
  return <Navigate to={`/app/${splat || ''}`} replace />
}


function Admin({ children }) {
  return <RequireAdmin>{children}</RequireAdmin>
}

function Feature({ feature, disabled, children }) {
  return <RequireFeature feature={feature} disabled={disabled}>{children}</RequireFeature>
}

const appChildren = [
  { index: true, element: <Home /> },
  { path: 'chat', element: <Chat /> },
  { path: 'group-chat', element: <Feature feature="chat"><GroupChat /></Feature> },
  { path: 'chat/:model', element: <Chat /> },
  { path: 'image', element: <ImageGen /> },
  { path: 'image/:model', element: <ImageGen /> },
  { path: 'video', element: <VideoGen /> },
  { path: 'video/:model', element: <VideoGen /> },
  { path: '3d', element: <Feature feature="3d"><ThreeDGen /></Feature> },
  { path: '3d/:model', element: <Feature feature="3d"><ThreeDGen /></Feature> },
  { path: 'tts', element: <TTS /> },
  { path: 'tts/:model', element: <TTS /> },
  { path: 'sound', element: <Sound /> },
  { path: 'sound/:model', element: <Sound /> },
  { path: 'diarization', element: <Feature feature="audio_diarization"><Diarization /></Feature> },
  { path: 'diarization/:model', element: <Feature feature="audio_diarization"><Diarization /></Feature> },
  { path: 'transform', element: <Feature feature="audio_transform"><AudioTransform /></Feature> },
  { path: 'transform/:model', element: <Feature feature="audio_transform"><AudioTransform /></Feature> },
  { path: 'studio', element: <Studio /> },
  // Tabs are path segments, not a query parameter: switching generator is
  // navigation, and ?tab= reads like a filter. Legacy ?tab= links redirect.
  { path: 'studio/:tab', element: <Studio /> },
  { path: 'talk', element: <Talk /> },
  { path: 'account', element: <Account /> },

  // Build hub: one tab bar over the tool landing pages. Deep create/edit/chat
  // flows below render full-width, without the bar.
  {
    element: <HubLayout config={buildHub} />,
    children: [
      { path: 'build', element: <BuildOverview /> },
      { path: 'agents', element: <Feature feature="agents"><Agents /></Feature> },
      { path: 'agents/:name', element: <Feature feature="agents"><AgentPage /></Feature> },
      { path: 'agents/:name/status', element: <Feature feature="agents"><AgentStatus /></Feature> },
      { path: 'skills', element: <Feature feature="skills"><Skills /></Feature> },
      { path: 'collections', element: <Feature feature="collections"><Collections /></Feature> },
      { path: 'collections/:name', element: <Feature feature="collections"><Collections /></Feature> },
      { path: 'agent-jobs', element: <Feature feature="mcp_jobs"><AgentJobs /></Feature> },
      { path: 'agent-jobs/tasks/:id', element: <Feature feature="mcp_jobs"><AgentTaskDetails /></Feature> },
      { path: 'fine-tune', element: <Feature feature="fine_tuning" disabled={<ToolOff tool="fineTune" />}><FineTune /></Feature> },
      { path: 'quantize', element: <Feature feature="quantization" disabled={<ToolOff tool="quantize" />}><Quantize /></Feature> },
      { path: 'face', element: <Feature feature="face_recognition" disabled={<FeatureOff feature="face" />}><FaceRecognition /></Feature> },
      { path: 'face/:model', element: <Feature feature="face_recognition" disabled={<FeatureOff feature="face" />}><FaceRecognition /></Feature> },
      { path: 'voice', element: <Feature feature="voice_recognition" disabled={<FeatureOff feature="voice" />}><VoiceRecognition /></Feature> },
      { path: 'voice/:model', element: <Feature feature="voice_recognition" disabled={<FeatureOff feature="voice" />}><VoiceRecognition /></Feature> },
      { path: 'voice-library', element: <Admin><VoiceRecognition tab="speech" /></Admin> },
      { path: 'import-model', element: <Admin><ImportModel /></Admin> },
    ],
  },
  // Build deep flows: full-width, no tab bar.
  { path: 'agents/new', element: <Feature feature="agents"><AgentCreate /></Feature> },
  { path: 'agents/:name/edit', element: <Feature feature="agents"><AgentCreate /></Feature> },
  { path: 'agents/:name/runs/:id', element: <Feature feature="agents"><AgentRun /></Feature> },
  { path: 'agents/:name/chat', element: <Feature feature="agents"><AgentChatRedirect /></Feature> },
  { path: 'skills/new', element: <Feature feature="skills"><SkillEdit /></Feature> },
  { path: 'skills/edit/:name', element: <Feature feature="skills"><SkillEdit /></Feature> },
  { path: 'agent-jobs/tasks/new', element: <Feature feature="mcp_jobs"><AgentTaskDetails /></Feature> },
  { path: 'agent-jobs/tasks/:id/edit', element: <Feature feature="mcp_jobs"><AgentTaskDetails /></Feature> },
  { path: 'agent-jobs/jobs/:id', element: <Feature feature="mcp_jobs"><AgentJobDetails /></Feature> },

  // Operate hub (admin): one tab bar over the runtime, cluster, traffic and
  // settings pages.
  {
    element: <HubLayout config={operateHub} />,
    children: [
      { path: 'operate', element: <Admin><OperateOverview /></Admin> },
      { path: 'backends', element: <Admin><Backends /></Admin> },
      { path: 'activity', element: <Admin><Activity /></Admin> },
      { path: 'settings', element: <Admin><Settings /></Admin> },
      { path: 'traffic', element: <Admin><TrafficOverview /></Admin> },
      { path: 'traffic/models', element: <Admin><TrafficModels /></Admin> },
      { path: 'traffic/host', element: <Admin><TrafficHost /></Admin> },
      { path: 'traffic/prometheus', element: <Admin><Prometheus /></Admin> },
      { path: 'traces', element: <Admin><Traces /></Admin> },
      { path: 'traces/:id', element: <Admin><TracePage /></Admin> },
      { path: 'backend-logs', element: <Admin><BackendLogs /></Admin> },
      { path: 'backend-logs/:modelId', element: <Admin><BackendLogs /></Admin> },
      { path: 'p2p', element: <Admin><P2P /></Admin> },
      { path: 'nodes', element: <Admin><Nodes /></Admin> },
      { path: 'nodes/add', element: <Admin><AddNode /></Admin> },
      { path: 'nodes/:id', element: <Admin><NodeDetail /></Admin> },
      { path: 'scheduling', element: <Admin><Scheduling /></Admin> },
      { path: 'node-backend-logs/:nodeId/:modelId', element: <Admin><NodeBackendLogs /></Admin> },
      { path: 'usage', element: <Usage /> },
      { path: 'users', element: <RequireAuthEnabled><Admin><Users /></Admin></RequireAuthEnabled> },
      { path: 'middleware', element: <Admin><Middleware /></Admin> },
      { path: 'failover', element: <Admin><Failover /></Admin> },
    ],
  },

  // Canonical resource pages and legacy management compatibility.
  // A model's own page is a child of the list, not a sibling: the list keeps
  // its filters, its sort and its scroll while the page is open, and Back finds
  // it as it was.
  {
    path: 'models',
    element: <Admin><Models /></Admin>,
    children: [{ path: ':id', element: <ModelPage /> }],
  },
  { path: 'manage', element: <Admin><ManageRedirect /></Admin> },
  { path: 'voice-library/new', element: <Admin><VoiceProfileCreate /></Admin> },
  { path: 'model-editor', element: <Admin><ModelEditor /></Admin> },
  { path: 'model-editor/:name', element: <Admin><ModelEditor /></Admin> },
  { path: '*', element: <NotFound /> },
]

export const router = createBrowserRouter([
  {
    path: '/login',
    element: <Login />,
  },
  {
    path: '/invite/:code',
    element: <Login />,
  },
  {
    path: '/explorer',
    element: <Explorer />,
  },
  {
    path: '/app',
    element: <RequireAuth><App /></RequireAuth>,
    children: appChildren,
  },
  // Backward compatibility: redirect /browse/* to /app/*
  {
    path: '/browse/*',
    element: <BrowseRedirect />,
  },
  {
    path: '/',
    element: <Navigate to="/app" replace />,
  },
  // An address outside the app that is not a page: the same 404 page, so the
  // visitor gets a way back instead of the router's default error.
  { path: '*', element: <NotFound /> },
], { basename: routerBasename })
