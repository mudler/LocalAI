import { chatPushApi } from './api'
import { urlBase64ToBytes } from './chatTrigger'

// Browser push for the inbox. The server keeps the subscription; the service
// worker shows the notification and opens its link.

export function pushSupported() {
  return typeof window !== 'undefined' && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

async function registration() {
  // Relative to the page, so a server behind a path prefix finds the worker.
  return navigator.serviceWorker.register(new URL('chats-sw.js', document.baseURI).href)
}

// 'unsupported', 'denied', 'off' or 'on'. A server that cannot keep
// subscriptions is 'unavailable'.
export async function pushState() {
  if (!pushSupported()) return 'unsupported'
  if (Notification.permission === 'denied') return 'denied'
  try {
    const reg = await navigator.serviceWorker.getRegistration(new URL('chats-sw.js', document.baseURI).href)
    const sub = await reg?.pushManager.getSubscription()
    return sub ? 'on' : 'off'
  } catch {
    return 'off'
  }
}

export async function enablePush() {
  const perm = await Notification.requestPermission()
  if (perm !== 'granted') return 'denied'
  const { public_key: key } = await chatPushApi.key()
  const reg = await registration()
  await navigator.serviceWorker.ready
  const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToBytes(key) })
  await chatPushApi.subscribe(sub.toJSON())
  return 'on'
}

export async function disablePush() {
  const reg = await navigator.serviceWorker.getRegistration(new URL('chats-sw.js', document.baseURI).href)
  const sub = await reg?.pushManager.getSubscription()
  if (sub) {
    await chatPushApi.unsubscribe(sub.endpoint).catch(() => {})
    await sub.unsubscribe()
  }
  return 'off'
}
