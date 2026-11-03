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
  // The module once a load has finished. A page that was preloaded then
  // renders at once: React.lazy takes a thenable that answers synchronously
  // as resolved, so the route does not suspend and flash its fallback (React
  // shows a fallback for at least 300 ms once it has shown one).
  let loaded = null
  const load = () => loader().then((mod) => { loaded = mod; return mod })
  // Several pages can share a segment (the chat page, connectors and conversion
  // under /app/chats/): a preload warms each of them, the chat page included.
  if (key !== null) (preloaders[key] ||= []).push(load)
  // preloadRoute keeps the raw loader: a hover that fails must stay silent
  // rather than reload the page out from under the pointer. The click that
  // follows goes through this wrapper and recovers there.
  return lazy(() => (loaded ? { then: (resolve) => resolve(loaded) } : load().catch(err => {
    // A reload is already committing: a second chunk failing in the same
    // document must wait for it, not throw and flash the error boundary.
    if (reloading) return new Promise(() => {})
    if (!claimReload()) throw err
    reloading = true
    window.location.reload()
    // Stay pending. Resolving or rejecting here would flash the error boundary
    // in the frames before the reload commits.
    return new Promise(() => {})
  })))
}

export function preloadRoute(path) {
  if (!path) return
  const m = path.match(/^\/app(?:\/([^/?#]*))?/)
  if (!m) return
  for (const load of preloaders[m[1] ?? ''] || []) load().catch(() => { /* network blip — real click will retry */ })
}

const Home = page('', () => import('./pages/Home'))
const Chats = page('chats', () => import('./pages/Chat'))
const AllChats = page(null, () => import('./pages/AllChats'))
const Workspace = page('workspace', () => import('./pages/Workspace'))
const Pages = page('pages', () => import('./pages/Pages'))
const PageView = page(null, () => import('./pages/PageView'))
const SharedPageView = page(null, () => import('./pages/SharedPageView'))
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
const Proposals = page('chats', () => import('./pages/Proposals'))
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
// The UI primitives in every state: a reference for developers and the
// fixture of e2e/ui-primitives.spec.js. Admin only and not in the navigation.
// Only referenced from JSX below.
// eslint-disable-next-line no-unused-vars
const UiCatalog = page(null, () => import('./pages/UiCatalog'))
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
import { bareChatsRedirect, chatsRedirect } from './utils/legacyRoutes'
import { legacyAddonsHub, operateHub } from './components/hub/hubConfig'
import { useHubAuth } from './components/hub/useHubAuth'

// The old chat page became the chats and the old agent-jobs pages became the
// triggers; the agents, skills and triggers pages left /app/chats/. An old
// address opens the matching page and keeps the query string and the hash.
function ChatsRedirect() {
  const { pathname, search, hash } = useLocation()
  return <Navigate to={chatsRedirect(pathname, search, hash)} replace />
}

// /app/chats/<id> is a transcript. /app/chats with no id is the empty chat
// only when the app hands something to it in the navigation state; as an
// address it opens Home (see bareChatsRedirect). The chat page stays mounted
// across the two, so a transcript that becomes the empty chat (its chat was
// deleted) keeps its undo offer.
function ChatsRoute() {
  const { pathname, search, hash, state } = useLocation()
  const to = bareChatsRedirect(pathname, search, hash, state)
  if (to) return <Navigate to={to} replace />
  return <Chats />
}

// The old status, run and chat pages of an agent are its page now.
function AgentRedirect() {
  const { id } = useParams()
  return <Navigate to={`/app/agents/${encodeURIComponent(id)}`} replace />
}

function BrowseRedirect() {
  const { '*': splat } = useParams()
  return <Navigate to={`/app/${splat || ''}`} replace />
}


function Admin({ children }) {
  return <RequireAdmin>{children}</RequireAdmin>
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
  { path: 'chats/proposals', element: <Feature feature="chats"><Proposals /></Feature> },
  // One route for a new chat and an open one, so the page stays mounted when a
  // chat starts or is left (a delete waiting for its Undo survives the move).
  { path: 'chats/all', element: <Feature feature="chats"><AllChats /></Feature> },
  { path: 'chats/:id?', element: <Feature feature="chats"><ChatsRoute /></Feature> },
  { path: 'files', element: <Feature feature="chats"><Workspace /></Feature> },
  { path: 'pages', element: <Feature feature="chats"><Feature feature="pages"><Pages /></Feature></Feature> },
  { path: 'pages/:name', element: <Feature feature="chats"><Feature feature="pages"><PageView /></Feature></Feature> },
  { path: 'pages/shared/:folder/:name', element: <Feature feature="chats"><Feature feature="pages"><SharedPageView /></Feature></Feature> },
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
  { path: 'model-editor', element: <Admin><ModelEditor /></Admin> },
  { path: 'model-editor/:name', element: <Admin><ModelEditor /></Admin> },
  { path: 'ui-catalog', element: <Admin><UiCatalog /></Admin> },
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
