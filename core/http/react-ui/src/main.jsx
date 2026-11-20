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
// The UI kit's CSS in its documented order (packages/ui-kit/README.md), with
// the app's global rules and its theme bridge in place of the kit defaults.
import '../packages/ui-kit/vendor/tokens.css'
import '../packages/ui-kit/themes/localai.css'
import '../packages/ui-kit/vendor/motion.css'
import '../packages/ui-kit/vendor/components.css'
import './index.css'
import './theme.css'
// The kit's rules sit between the tokens and the app's own rules: a page's
// class on the same element still wins over a kit component's base rule.
import '../packages/ui-kit/css/kit.css'
import './App.css'
import './components/remotes/remotes.css'
import { ShellFallback } from './components/RouteFallback'

// The first paint is the app shell with placeholders, not a blank window
// with a spinner, while the translations and the session load.
function BootFallback() {
  return <ShellFallback />
}

// ThemeProvider sits inside AuthProvider because the theme is kept per
// account. BrandingProvider sits outside AuthProvider so the login screen — which
// renders before authentication completes — can pick up the configured
// instance name and logo from the public /api/branding endpoint.
createRoot(document.getElementById('root')).render(
  <StrictMode>
    <Suspense fallback={<BootFallback />}>
      <BrandingProvider>
        <AuthProvider>
          <ThemeProvider>
            <OperationsProvider>
              <RouterProvider router={router} />
            </OperationsProvider>
          </ThemeProvider>
        </AuthProvider>
      </BrandingProvider>
    </Suspense>
  </StrictMode>,
)
