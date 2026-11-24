import { createRoot } from 'react-dom/client'
import './styles'
import { App } from './App'

createRoot(document.getElementById('root')!).render(<App bridge={window.launcher} />)
