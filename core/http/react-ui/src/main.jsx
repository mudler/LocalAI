import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from 'react-router-dom'
import { ThemeProvider } from './contexts/ThemeContext'
import { BrandingProvider } from './contexts/BrandingContext'
import { AuthProvider } from './context/AuthContext'
import { router } from './router'
import '@fortawesome/fontawesome-free/css/all.min.css'
import './index.css'
import './theme.css'
import './App.css'

// BrandingProvider sits outside AuthProvider so the login screen — which
// renders before authentication completes — can pick up the configured
// instance name and logo from the public /api/branding endpoint.
createRoot(document.getElementById('root')).render(
  <StrictMode>
    <ThemeProvider>
      <BrandingProvider>
        <AuthProvider>
          <RouterProvider router={router} />
        </AuthProvider>
      </BrandingProvider>
    </ThemeProvider>
  </StrictMode>,
)
