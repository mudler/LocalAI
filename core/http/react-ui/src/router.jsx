import { lazy } from 'react'
import { createBrowserRouter, Navigate, Outlet, useLocation, useOutletContext, useParams } from 'react-router-dom'
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
const Chats = page('chats', () => import('./pages/Chat'))
const Workspace = page('workspace', () => import('./pages/Workspace'))
const Pages = page('pages', () => import('./pages/Pages'))
const PageView = page(null, () => import('./pages/PageView'))
const Agents = page('agents', () => import('./pages/Agents'))
const AgentCreate = page(null, () => import('./pages/AgentCreate'))
const AgentPage = page(null, () => import('./pages/AgentPage'))
const Skills = page('skills', () => import('./pages/Skills'))
const SkillEdit = page(null, () => import('./pages/SkillEdit'))
const AgentJobs = page('triggers', () => import('./pages/AgentJobs'))
const AgentTaskDetails = page(null, () => import('./pages/AgentTaskDetails'))
const AgentTasks = page('agent-tasks', () => import('./pages/AgentTasks'))
const ChatsAdmin = page('chats-admin', () => import('./pages/ChatsAdmin'))
const ChatConnectors = page('chats', () => import('./pages/ChatConnectors'))
const ChatConversion = page('chats', () => import('./pages/ChatConversion'))
const Models = page('models', () => import('./pages/Models'))
const ManageRedirect = page('manage', () => import('./pages/ManageRedirect'))
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
const Collections = page('collections', () => import('./pages/Collections'))
const ModelEditor = page(null, () => import('./pages/ModelEditor'))
// Rendered by Models, through its <Outlet/>: the list stays mounted behind it.
// eslint-disable-next-line no-unused-vars
const ModelPage = page(null, () => import('./pages/ModelPage'))
// PipelineEditor removed — the Model Editor with templates handles all model types
const ImportModel = page(null, () => import('./pages/ImportModel'))
const BackendLogs = page(null, () => import('./pages/BackendLogs'))
const Explorer = page(null, () => import('./pages/Explorer'))
const Login = page(null, () => import('./pages/Login'))
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
const Addons = page('addons', () => import('./pages/Addons'))
// Only referenced from JSX below, which eslint cannot see.
// eslint-disable-next-line no-unused-vars
const AddonRoute = page(null, () => import('./addons/AddonRoute'))
// Only referenced from JSX below, which eslint cannot see.
// eslint-disable-next-line no-unused-vars
const MCPConnectors = page('mcp-connectors', () => import('./pages/MCPConnectors'))

import HubLayout from './components/hub/HubLayout'
// Only referenced from JSX below, which eslint cannot see.
// eslint-disable-next-line no-unused-vars
import { AddonRegistryProvider } from './addons/registry'
import { useAuth } from './context/AuthContext'
import { NavigationProvider, useNavigation } from './context/NavigationContext'
import { addonPageAddress, chatsRedirect, studioAddress, studioRedirect, talkAddress } from './utils/legacyRoutes'
import { legacyAddonsHub, operateHub } from './components/hub/hubConfig'
import { useHubAuth } from './components/hub/useHubAuth'

// The old chat page became the chats and the old agent-jobs pages became the
// triggers; the agents, skills and triggers pages left /app/chats/. An old
// address opens the matching page and keeps the query string and the hash.
function ChatsRedirect() {
  const { pathname, search, hash } = useLocation()
  return <Navigate to={chatsRedirect(pathname, search, hash)} replace />
}

// The old status, run and chat pages of an agent are its page now.
function AgentRedirect() {
  const { id } = useParams()
  return <Navigate to={`/app/agents/${encodeURIComponent(id)}`} replace />
}

// An old generator link opens the same generator inside the Studio add-on. The
// model, the query string and the hash are kept.
function StudioTabRedirect({ legacy }) {
  const { model } = useParams()
  const { search, hash } = useLocation()
  return <Navigate to={studioRedirect(legacy, model, search, hash)} replace />
}

// /app/studio[/tab[/model]] is a page of the Studio add-on now.
function StudioRedirect() {
  const { tab, model } = useParams()
  const { search, hash } = useLocation()
  return <Navigate to={studioAddress({ tab, model, search, hash })} replace />
}

// /app/talk is a page of the Talk add-on now.
function TalkRedirect() {
  const { search, hash } = useLocation()
  return <Navigate to={talkAddress({ search, hash })} replace />
}

function BrowseRedirect() {
  const { '*': splat } = useParams()
  return <Navigate to={`/app/${splat || ''}`} replace />
}


// The pages of Face, Voice, Fine-tuning and Quantization live only in their
// add-ons. The old address opens the add-on page; when the add-on is not
// installed or not ready, the add-on shell says so. withModel carries the
// :model segment of the old route as ?model=<id>: an add-on path segment
// refuses "/", ":" and "%" and long ids (resolveSubpath), so a model id cannot
// travel in the path.
function AddonRedirect({ addon, page, withModel }) {
  const { search, hash } = useLocation()
  const { model } = useParams()
  return <Navigate to={addonPageAddress({ addon, page, model: withModel ? model : undefined, search, hash })} replace />
}

function Admin({ children }) {
  return <RequireAdmin>{children}</RequireAdmin>
}

// The layout of the add-on routes: the Build hub bar under the legacy switch,
// the bare page otherwise.
// With add-ons off (features.addons === false) the add-on addresses and the
// old fine-tuning and quantization addresses lead back to /app. An absent key
// counts as on.
// Home then says that add-ons are off and how to turn them on (state.addonsOff
// names the add-on the address was for).
function AddonsOffRedirect({ addon, children }) {
  const { features, featuresLoaded } = useHubAuth()
  if (!featuresLoaded) return null
  if (features.addons === false) return <Navigate to="/app" replace state={{ addonsOff: addon }} />
  return children
}

function AddonsShell() {
  const { features, featuresLoaded } = useHubAuth()
  const outletContext = useOutletContext()
  const { pathname } = useLocation()
  if (!featuresLoaded) return null
  if (features.addons === false) {
    const addon = /\/app\/addons\/([^/]+)/.exec(pathname)?.[1] || 'addons'
    return <Navigate to="/app" replace state={{ addonsOff: addon }} />
  }
  if (features.addon_navigation === false) return <HubLayout config={legacyAddonsHub} />
  return <Outlet context={outletContext} />
}

function DrawerGate({ children }) {
  const { features } = useHubAuth()
  const legacy = features.addon_navigation === false
  const { isAdmin, hasFeature, loading } = useAuth()
  const { builtin, status } = useNavigation()
  if (legacy) return <Feature feature="addons">{children}</Feature>
  if (loading || status === 'idle' || status === 'loading') return null
  if (!isAdmin && !hasFeature('addons') && builtin.length === 0) return <Navigate to="/app" replace />
  return children
}

// A page of a part the server can switch off (/api/features): with it off the
// address leads to `to`. An absent key counts as on.
function ServerSwitch({ name, to, children }) {
  const { features, featuresLoaded } = useHubAuth()
  if (!featuresLoaded) return null
  if (features[name] === false) return <Navigate to={to} replace />
  return children
}

function Feature({ feature, disabled, children }) {
  return <RequireFeature feature={feature} disabled={disabled}>{children}</RequireFeature>
}

const appChildren = [
  { index: true, element: <Home /> },
  { path: 'chat', element: <ChatsRedirect /> },
  { path: 'chat/:model', element: <ChatsRedirect /> },
  { path: 'chats/agents', element: <ChatsRedirect /> },
  { path: 'chats/skills', element: <ChatsRedirect /> },
  { path: 'chats/triggers', element: <ChatsRedirect /> },
  { path: 'chats/connectors', element: <Feature feature="connectors"><ChatConnectors /></Feature> },
  { path: 'chats/conversion', element: <Feature feature="chats"><ChatConversion /></Feature> },
  // One route for a new chat and an open one, so the page stays mounted when a
  // chat starts or is left (a delete waiting for its Undo survives the move).
  { path: 'chats/:id?', element: <Feature feature="chats"><Chats /></Feature> },
  { path: 'files', element: <Feature feature="chats"><Workspace /></Feature> },
  { path: 'pages', element: <Feature feature="chats"><Feature feature="pages"><Pages /></Feature></Feature> },
  { path: 'pages/:name', element: <Feature feature="chats"><Feature feature="pages"><PageView /></Feature></Feature> },
  { path: 'agents', element: <Feature feature="agents"><Agents /></Feature> },
  { path: 'agents/new', element: <Feature feature="agents"><AgentCreate /></Feature> },
  { path: 'agents/:id/edit', element: <Feature feature="agents"><AgentCreate /></Feature> },
  { path: 'agents/:id', element: <Feature feature="agents"><AgentPage /></Feature> },
  { path: 'agents/:id/*', element: <AgentRedirect /> },
  { path: 'skills', element: <Feature feature="chats"><Skills /></Feature> },
  { path: 'skills/new', element: <Feature feature="chats"><SkillEdit /></Feature> },
  { path: 'skills/edit/:layer/:name', element: <Feature feature="chats"><SkillEdit /></Feature> },
  { path: 'triggers', element: <Feature feature="triggers"><AgentJobs /></Feature> },
  { path: 'triggers/new', element: <Feature feature="triggers"><AgentTaskDetails /></Feature> },
  { path: 'triggers/:id/edit', element: <Feature feature="triggers"><AgentTaskDetails /></Feature> },
  { path: 'triggers/:id', element: <Feature feature="triggers"><AgentTaskDetails /></Feature> },
  { path: 'agent-tasks', element: <Feature feature="mcp_jobs"><ServerSwitch name="agent_jobs" to="/app/agents"><AgentTasks /></ServerSwitch></Feature> },
  { path: 'image', element: <StudioTabRedirect legacy="image" /> },
  { path: 'image/:model', element: <StudioTabRedirect legacy="image" /> },
  { path: 'video', element: <StudioTabRedirect legacy="video" /> },
  { path: 'video/:model', element: <StudioTabRedirect legacy="video" /> },
  { path: '3d', element: <Feature feature="3d"><StudioTabRedirect legacy="3d" /></Feature> },
  { path: '3d/:model', element: <Feature feature="3d"><StudioTabRedirect legacy="3d" /></Feature> },
  { path: 'tts', element: <StudioTabRedirect legacy="tts" /> },
  { path: 'tts/:model', element: <StudioTabRedirect legacy="tts" /> },
  { path: 'sound', element: <StudioTabRedirect legacy="sound" /> },
  { path: 'sound/:model', element: <StudioTabRedirect legacy="sound" /> },
  { path: 'diarization', element: <Feature feature="audio_diarization"><StudioTabRedirect legacy="diarization" /></Feature> },
  { path: 'diarization/:model', element: <Feature feature="audio_diarization"><StudioTabRedirect legacy="diarization" /></Feature> },
  { path: 'transform', element: <Feature feature="audio_transform"><StudioTabRedirect legacy="transform" /></Feature> },
  { path: 'transform/:model', element: <Feature feature="audio_transform"><StudioTabRedirect legacy="transform" /></Feature> },
  // Studio and Talk are add-ons; the old addresses open them. When the add-on
  // is not installed the add-on page says so.
  { path: 'studio', element: <StudioRedirect /> },
  { path: 'studio/:tab', element: <StudioRedirect /> },
  // A generator opened on one model: /app/studio/images/<model>.
  { path: 'studio/:tab/:model', element: <StudioRedirect /> },
  { path: 'talk', element: <TalkRedirect /> },
  { path: 'account', element: <Account /> },
  // The Add-ons drawer. A viewer without the addons feature still reaches it
  // when a built-in tile is theirs to open (the built-in list is empty today).
  // With the legacy switch on, both routes sit under the legacy Add-ons hub,
  // and need the addons feature as they did before.
  {
    element: <AddonsShell />,
    children: [
      { path: 'addons', element: <DrawerGate><Addons /></DrawerGate> },
      { path: 'addons/:addonId/*', element: <Feature feature="addons"><AddonRoute /></Feature> },
    ],
  },

  // The Build hub is gone: its pages are top-level now. Memory (collections)
  // and the agents have their own sidebar items, Import is part of adding a
  // model, and fine-tuning, quantization, faces and voices are add-ons. The old
  // landing address opens the models.
  { path: 'build', element: <Navigate to="/app/models" replace /> },
  { path: 'collections', element: <Feature feature="collections"><Collections /></Feature> },
  { path: 'collections/:name', element: <Feature feature="collections"><Collections /></Feature> },
  { path: 'fine-tune', element: <AddonsOffRedirect addon="fine-tuning"><AddonRedirect addon="fine-tuning" page="jobs" /></AddonsOffRedirect> },
  { path: 'quantize', element: <AddonsOffRedirect addon="quantization"><AddonRedirect addon="quantization" page="jobs" /></AddonsOffRedirect> },
  { path: 'face', element: <AddonRedirect addon="face" page="recognition" /> },
  { path: 'face/:model', element: <AddonRedirect addon="face" page="recognition" withModel /> },
  { path: 'voice', element: <AddonRedirect addon="voice" page="voice/recognition" /> },
  { path: 'voice/:model', element: <AddonRedirect addon="voice" page="voice/recognition" withModel /> },
  { path: 'voice-library', element: <AddonRedirect addon="voice" page="voice/library" /> },
  { path: 'import-model', element: <Admin><ImportModel /></Admin> },
  // The user's MCP connectors have a page of their own; the old address was a
  // tab of the messaging connectors.
  { path: 'mcp-connectors', element: <Feature feature="chats"><MCPConnectors /></Feature> },
  // The old agent-jobs pages are the triggers now.
  { path: 'agent-jobs/*', element: <ChatsRedirect /> },
  { path: 'agent-jobs', element: <ChatsRedirect /> },

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
      { path: 'chats-admin', element: <Admin><ServerSwitch name="chats" to="/app/operate"><ChatsAdmin /></ServerSwitch></Admin> },
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
  { path: 'voice-library/new', element: <AddonRedirect addon="voice" page="voice/library/new" /> },
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
    element: <RequireAuth><AddonRegistryProvider><NavigationProvider><App /></NavigationProvider></AddonRegistryProvider></RequireAuth>,
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
