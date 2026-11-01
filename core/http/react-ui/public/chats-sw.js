// Service worker of browser push for chats. It shows the title of a
// notification and opens its link; the payload carries nothing else.
self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch (_) {
    data = {}
  }
  const title = typeof data.title === 'string' && data.title ? data.title : 'LocalAI'
  event.waitUntil(self.registration.showNotification(title, { data: { link: typeof data.link === 'string' ? data.link : '' }, tag: data.link || undefined }))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const link = (event.notification.data && event.notification.data.link) || ''
  // The scope is the root of the server, which may sit under a proxy prefix.
  const target = new URL(link.replace(/^\//, '') || 'app/', self.registration.scope).href
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((wins) => {
      for (const w of wins) {
        if (w.url === target && 'focus' in w) return w.focus()
      }
      return self.clients.openWindow(target)
    }),
  )
})
