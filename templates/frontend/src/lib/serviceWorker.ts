// Service worker registration helper.
//
// Registers the reference worker in public/sw.js, which caches content-hashed
// immutable assets cache-first and the navigation shell network-first. Call
// registerServiceWorker() once from your app entry (e.g. main.tsx) in production.
export type RegisterServiceWorkerOptions = {
  // Path to the worker script. Defaults to '/sw.js'.
  scriptUrl?: string
  // Only register in production by default; pass true to force in dev.
  enableInDev?: boolean
}

export const registerServiceWorker = (options: RegisterServiceWorkerOptions = {}): void => {
  if (typeof window === 'undefined' || !('serviceWorker' in navigator)) return
  if (!options.enableInDev && import.meta.env?.DEV) return

  const scriptUrl = options.scriptUrl ?? '/sw.js'
  window.addEventListener('load', () => {
    navigator.serviceWorker.register(scriptUrl).catch((error) => {
      console.warn('[sw] registration failed:', error)
    })
  })
}

export const unregisterServiceWorkers = async (): Promise<void> => {
  if (typeof window === 'undefined' || !('serviceWorker' in navigator)) return
  const registrations = await navigator.serviceWorker.getRegistrations()
  await Promise.all(registrations.map((registration) => registration.unregister()))
}
