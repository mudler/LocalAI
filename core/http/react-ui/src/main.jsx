import { StrictMode, Suspense } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from 'react-router-dom'
import { ThemeProvider } from './contexts/ThemeContext'
import { BrandingProvider } from './contexts/BrandingContext'
import { AuthProvider } from './context/AuthContext'
import { OperationsProvider } from './contexts/OperationsContext'
import { router } from './router'
import './i18n'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import './vendor/ui-kit/tokens.css'
import './theme-localai.css'
import './vendor/ui-kit/motion.css'
import './vendor/ui-kit/components.css'
import './index.css'
import './theme.css'
import './App.css'
import LoadingSpinner from './components/LoadingSpinner'

function BootFallback() {
  return (
    <div className="app-boot-spinner">
      <LoadingSpinner size="boot" />
    </div>
  )
}

// BrandingProvider sits outside AuthProvider so the login screen — which
// renders before authentication completes — can pick up the configured
// instance name and logo from the public /api/branding endpoint.
createRoot(document.getElementById('root')).render(
  <StrictMode>
    <Suspense fallback={<BootFallback />}>
      <ThemeProvider>
        <BrandingProvider>
          <AuthProvider>
            <OperationsProvider>
              <RouterProvider router={router} />
            </OperationsProvider>
          </AuthProvider>
        </BrandingProvider>
      </ThemeProvider>
    </Suspense>
  </StrictMode>,
)
