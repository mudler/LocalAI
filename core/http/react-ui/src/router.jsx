import { createBrowserRouter, Navigate, useParams } from 'react-router-dom'
import { routerBasename } from './utils/basePath'
import App from './App'
import Home from './pages/Home'
import Chat from './pages/Chat'
import Models from './pages/Models'
import Manage from './pages/Manage'
import ImageGen from './pages/ImageGen'
import VideoGen from './pages/VideoGen'
import TTS from './pages/TTS'
import Sound from './pages/Sound'
import Talk from './pages/Talk'
import Backends from './pages/Backends'
import Settings from './pages/Settings'
import Traces from './pages/Traces'
import P2P from './pages/P2P'
import Agents from './pages/Agents'
import AgentCreate from './pages/AgentCreate'
import AgentChat from './pages/AgentChat'
import AgentStatus from './pages/AgentStatus'
import Collections from './pages/Collections'
import CollectionDetails from './pages/CollectionDetails'
import Skills from './pages/Skills'
import SkillEdit from './pages/SkillEdit'
import AgentJobs from './pages/AgentJobs'
import AgentTaskDetails from './pages/AgentTaskDetails'
import AgentJobDetails from './pages/AgentJobDetails'
import ModelEditor from './pages/ModelEditor'
import PipelineEditor from './pages/PipelineEditor'
import ImportModel from './pages/ImportModel'
import BackendLogs from './pages/BackendLogs'
import Explorer from './pages/Explorer'
import Login from './pages/Login'
import FineTune from './pages/FineTune'
import Studio from './pages/Studio'
import NotFound from './pages/NotFound'
import Usage from './pages/Usage'
import Users from './pages/Users'
import Account from './pages/Account'
import RequireAdmin from './components/RequireAdmin'
import RequireAuth from './components/RequireAuth'
import RequireFeature from './components/RequireFeature'

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
  { path: 'models', element: <Admin><Models /></Admin> },
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
  { path: 'studio', element: <Studio /> },
  { path: 'talk', element: <Talk /> },
  { path: 'usage', element: <Usage /> },
  { path: 'account', element: <Account /> },
  { path: 'users', element: <Admin><Users /></Admin> },
  { path: 'manage', element: <Admin><Manage /></Admin> },
  { path: 'backends', element: <Admin><Backends /></Admin> },
  { path: 'settings', element: <Admin><Settings /></Admin> },
  { path: 'traces', element: <Admin><Traces /></Admin> },
  { path: 'backend-logs/:modelId', element: <Admin><BackendLogs /></Admin> },
  { path: 'p2p', element: <Admin><P2P /></Admin> },
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
  { path: 'model-editor/:name', element: <Admin><ModelEditor /></Admin> },
  { path: 'pipeline-editor', element: <Admin><PipelineEditor /></Admin> },
  { path: 'pipeline-editor/:name', element: <Admin><PipelineEditor /></Admin> },
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
