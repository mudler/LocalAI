import { lazy } from 'react'
import { createBrowserRouter, Navigate, useParams } from 'react-router-dom'
import { routerBasename } from './utils/basePath'
import App from './App'
import RequireAdmin from './components/RequireAdmin'
import RequireAuth from './components/RequireAuth'
import RequireAuthEnabled from './components/RequireAuthEnabled'
import RequireFeature from './components/RequireFeature'

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
function page(key, loader) {
  if (key !== null) preloaders[key] = loader
  return lazy(loader)
}

export function preloadRoute(path) {
  if (!path) return
  const m = path.match(/^\/app(?:\/([^/?#]*))?/)
  if (!m) return
  preloaders[m[1] ?? '']?.().catch(() => { /* network blip — real click will retry */ })
}

const Home = page('', () => import('./pages/Home'))
const Chat = page('chat', () => import('./pages/Chat'))
const Models = page('models', () => import('./pages/Models'))
const Manage = page('manage', () => import('./pages/Manage'))
const ImageGen = page('image', () => import('./pages/ImageGen'))
const VideoGen = page('video', () => import('./pages/VideoGen'))
const TTS = page('tts', () => import('./pages/TTS'))
const Sound = page('sound', () => import('./pages/Sound'))
const AudioTransform = page('transform', () => import('./pages/AudioTransform'))
const Talk = page('talk', () => import('./pages/Talk'))
const Backends = page('backends', () => import('./pages/Backends'))
const Settings = page('settings', () => import('./pages/Settings'))
const Traces = page('traces', () => import('./pages/Traces'))
const P2P = page('p2p', () => import('./pages/P2P'))
const Agents = page('agents', () => import('./pages/Agents'))
const AgentCreate = page(null, () => import('./pages/AgentCreate'))
const AgentChat = page(null, () => import('./pages/AgentChat'))
const AgentStatus = page(null, () => import('./pages/AgentStatus'))
const Collections = page('collections', () => import('./pages/Collections'))
const CollectionDetails = page(null, () => import('./pages/CollectionDetails'))
const Skills = page('skills', () => import('./pages/Skills'))
const SkillEdit = page(null, () => import('./pages/SkillEdit'))
const AgentJobs = page('agent-jobs', () => import('./pages/AgentJobs'))
const AgentTaskDetails = page(null, () => import('./pages/AgentTaskDetails'))
const AgentJobDetails = page(null, () => import('./pages/AgentJobDetails'))
const ModelEditor = page(null, () => import('./pages/ModelEditor'))
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
const Nodes = page('nodes', () => import('./pages/Nodes'))
const NodeBackendLogs = page(null, () => import('./pages/NodeBackendLogs'))
const NotFound = page(null, () => import('./pages/NotFound'))
const Usage = page('usage', () => import('./pages/Usage'))
const Users = page('users', () => import('./pages/Users'))
const Middleware = page('middleware', () => import('./pages/Middleware'))
const Account = page('account', () => import('./pages/Account'))

import AdminConsoleLayout from './components/AdminConsole/AdminConsoleLayout'

function BrowseRedirect() {
  const { '*': splat } = useParams()
  return <Navigate to={`/app/${splat || ''}`} replace />
}


function Admin({ children }) {
  return <RequireAdmin>{children}</RequireAdmin>
}

function Feature({ feature, children }) {
  return <RequireFeature feature={feature}>{children}</RequireFeature>
}

const appChildren = [
  { index: true, element: <Home /> },
  { path: 'chat', element: <Chat /> },
  { path: 'chat/:model', element: <Chat /> },
  { path: 'image', element: <ImageGen /> },
  { path: 'image/:model', element: <ImageGen /> },
  { path: 'video', element: <VideoGen /> },
  { path: 'video/:model', element: <VideoGen /> },
  { path: 'tts', element: <TTS /> },
  { path: 'tts/:model', element: <TTS /> },
  { path: 'sound', element: <Sound /> },
  { path: 'sound/:model', element: <Sound /> },
  { path: 'transform', element: <Feature feature="audio_transform"><AudioTransform /></Feature> },
  { path: 'transform/:model', element: <Feature feature="audio_transform"><AudioTransform /></Feature> },
  { path: 'studio', element: <Studio /> },
  { path: 'talk', element: <Talk /> },
  { path: 'face', element: <Feature feature="face_recognition"><FaceRecognition /></Feature> },
  { path: 'face/:model', element: <Feature feature="face_recognition"><FaceRecognition /></Feature> },
  { path: 'voice', element: <Feature feature="voice_recognition"><VoiceRecognition /></Feature> },
  { path: 'voice/:model', element: <Feature feature="voice_recognition"><VoiceRecognition /></Feature> },
  { path: 'account', element: <Account /> },
  {
    element: <AdminConsoleLayout />,
    children: [
      { path: 'models', element: <Admin><Models /></Admin> },
      { path: 'backends', element: <Admin><Backends /></Admin> },
      { path: 'settings', element: <Admin><Settings /></Admin> },
      { path: 'traces', element: <Admin><Traces /></Admin> },
      { path: 'backend-logs/:modelId', element: <Admin><BackendLogs /></Admin> },
      { path: 'p2p', element: <Admin><P2P /></Admin> },
      { path: 'nodes', element: <Admin><Nodes /></Admin> },
      { path: 'node-backend-logs/:nodeId/:modelId', element: <Admin><NodeBackendLogs /></Admin> },
      { path: 'usage', element: <Usage /> },
      { path: 'users', element: <RequireAuthEnabled><Admin><Users /></Admin></RequireAuthEnabled> },
      { path: 'middleware', element: <Admin><Middleware /></Admin> },
      { path: 'manage', element: <Admin><Manage /></Admin> },
    ],
  },
  { path: 'agents', element: <Feature feature="agents"><Agents /></Feature> },
  { path: 'agents/new', element: <Feature feature="agents"><AgentCreate /></Feature> },
  { path: 'agents/:name/edit', element: <Feature feature="agents"><AgentCreate /></Feature> },
  { path: 'agents/:name/chat', element: <Feature feature="agents"><AgentChat /></Feature> },
  { path: 'agents/:name/status', element: <Feature feature="agents"><AgentStatus /></Feature> },
  { path: 'collections', element: <Feature feature="collections"><Collections /></Feature> },
  { path: 'collections/:name', element: <Feature feature="collections"><CollectionDetails /></Feature> },
  { path: 'skills', element: <Feature feature="skills"><Skills /></Feature> },
  { path: 'skills/new', element: <Feature feature="skills"><SkillEdit /></Feature> },
  { path: 'skills/edit/:name', element: <Feature feature="skills"><SkillEdit /></Feature> },
  { path: 'agent-jobs', element: <Feature feature="mcp_jobs"><AgentJobs /></Feature> },
  { path: 'agent-jobs/tasks/new', element: <Feature feature="mcp_jobs"><AgentTaskDetails /></Feature> },
  { path: 'agent-jobs/tasks/:id', element: <Feature feature="mcp_jobs"><AgentTaskDetails /></Feature> },
  { path: 'agent-jobs/tasks/:id/edit', element: <Feature feature="mcp_jobs"><AgentTaskDetails /></Feature> },
  { path: 'agent-jobs/jobs/:id', element: <Feature feature="mcp_jobs"><AgentJobDetails /></Feature> },
  { path: 'fine-tune', element: <Feature feature="fine_tuning"><FineTune /></Feature> },
  { path: 'quantize', element: <Feature feature="quantization"><Quantize /></Feature> },
  { path: 'model-editor', element: <Admin><ModelEditor /></Admin> },
  { path: 'model-editor/:name', element: <Admin><ModelEditor /></Admin> },
  { path: 'import-model', element: <Admin><ImportModel /></Admin> },
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
], { basename: routerBasename })
