import { createBrowserRouter } from 'react-router-dom'
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
import AgentJobs from './pages/AgentJobs'
import AgentTaskDetails from './pages/AgentTaskDetails'
import AgentJobDetails from './pages/AgentJobDetails'
import ModelEditor from './pages/ModelEditor'
import ImportModel from './pages/ImportModel'
import Explorer from './pages/Explorer'
import Login from './pages/Login'
import NotFound from './pages/NotFound'

export const router = createBrowserRouter([
  {
    path: '/login',
    element: <Login />,
  },
  {
    path: '/explorer',
    element: <Explorer />,
  },
  {
    path: '/',
    element: <App />,
    children: [
      { index: true, element: <Home /> },
      { path: 'browse', element: <Models /> },
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
      { path: 'talk', element: <Talk /> },
      { path: 'manage', element: <Manage /> },
      { path: 'backends', element: <Backends /> },
      { path: 'settings', element: <Settings /> },
      { path: 'traces', element: <Traces /> },
      { path: 'p2p', element: <P2P /> },
      { path: 'agent-jobs', element: <AgentJobs /> },
      { path: 'agent-jobs/tasks/new', element: <AgentTaskDetails /> },
      { path: 'agent-jobs/tasks/:id', element: <AgentTaskDetails /> },
      { path: 'agent-jobs/tasks/:id/edit', element: <AgentTaskDetails /> },
      { path: 'agent-jobs/jobs/:id', element: <AgentJobDetails /> },
      { path: 'model-editor/:name', element: <ModelEditor /> },
      { path: 'import-model', element: <ImportModel /> },
      { path: '*', element: <NotFound /> },
    ],
  },
], { basename: '/app' })
