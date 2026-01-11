/**
 * LocalAI SPA Router
 * Client-side routing for the single-page application
 */

// Define routes and their corresponding view IDs
const SPA_ROUTES = {
  'home': { title: 'LocalAI', viewId: 'view-home', paths: ['/', ''] },
  'chat': { title: 'LocalAI - Chat', viewId: 'view-chat', paths: ['/chat'] },
  'text2image': { title: 'LocalAI - Images', viewId: 'view-text2image', paths: ['/text2image'] },
  'tts': { title: 'LocalAI - TTS', viewId: 'view-tts', paths: ['/tts'] },
  'talk': { title: 'LocalAI - Talk', viewId: 'view-talk', paths: ['/talk'] },
  'manage': { title: 'LocalAI - System', viewId: 'view-manage', paths: ['/manage'] },
  'browse': { title: 'LocalAI - Model Gallery', viewId: 'view-browse', paths: ['/browse'] }
};

// Parse URL path to determine route
function parseUrlPath(pathname) {
  // Remove trailing slash
  pathname = pathname.replace(/\/$/, '') || '/';
  
  // Check for hash-based routes first (for SPA navigation)
  const hash = window.location.hash.slice(1);
  if (hash) {
    const hashParts = hash.split('/');
    const route = hashParts[0];
    const model = hashParts[1] || null;
    if (SPA_ROUTES[route]) {
      return { route, params: model ? { model } : {} };
    }
  }
  
  // Check path-based routes
  for (const [route, config] of Object.entries(SPA_ROUTES)) {
    for (const path of config.paths) {
      if (pathname === path) {
        return { route, params: {} };
      }
      // Check for parameterized routes like /chat/:model
      if (pathname.startsWith(path + '/')) {
        const param = pathname.slice(path.length + 1);
        if (param) {
          return { route, params: { model: param } };
        }
      }
    }
  }
  
  // Default to home
  return { route: 'home', params: {} };
}

// Initialize the router store for Alpine.js
document.addEventListener('alpine:init', () => {
  // Parse initial route from URL
  const initialRoute = parseUrlPath(window.location.pathname);
  
  Alpine.store('router', {
    currentRoute: initialRoute.route,
    routeParams: initialRoute.params,
    previousRoute: null,
    
    /**
     * Navigate to a route
     * @param {string} route - The route name to navigate to
     * @param {Object} params - Optional parameters for the route
     */
    navigate(route, params = {}) {
      if (!SPA_ROUTES[route]) {
        console.warn(`Unknown route: ${route}`);
        return;
      }
      
      this.previousRoute = this.currentRoute;
      this.currentRoute = route;
      this.routeParams = params;
      
      // Update document title
      document.title = SPA_ROUTES[route].title;
      
      // Update URL without page reload using history API
      const url = route === 'home' ? '/' : `/#${route}`;
      if (params.model) {
        window.history.pushState({ route, params }, '', `/#${route}/${params.model}`);
      } else {
        window.history.pushState({ route, params }, '', url);
      }
      
      // Scroll to top on navigation
      window.scrollTo(0, 0);
      
      // Emit custom event for route change listeners
      window.dispatchEvent(new CustomEvent('spa:navigate', { 
        detail: { route, params, previousRoute: this.previousRoute } 
      }));
    },
    
    /**
     * Check if the current route matches
     * @param {string} route - The route to check
     * @returns {boolean}
     */
    isRoute(route) {
      return this.currentRoute === route;
    },
    
    /**
     * Navigate to chat with a specific model
     * @param {string} model - The model name
     */
    navigateToChat(model) {
      this.navigate('chat', { model });
    },
    
    /**
     * Navigate to text2image with a specific model
     * @param {string} model - The model name
     */
    navigateToText2Image(model) {
      this.navigate('text2image', { model });
    },
    
    /**
     * Navigate to TTS with a specific model
     * @param {string} model - The model name
     */
    navigateToTTS(model) {
      this.navigate('tts', { model });
    }
  });
});

// Handle browser back/forward buttons
window.addEventListener('popstate', (event) => {
  if (event.state && event.state.route) {
    Alpine.store('router').currentRoute = event.state.route;
    Alpine.store('router').routeParams = event.state.params || {};
  } else {
    // Parse URL for route
    const parsed = parseUrlPath(window.location.pathname);
    Alpine.store('router').currentRoute = parsed.route;
    Alpine.store('router').routeParams = parsed.params;
  }
});

// Export for use in other scripts
window.SPA_ROUTES = SPA_ROUTES;
window.parseUrlPath = parseUrlPath;
