// Reference service worker.
//
// Lessons encoded (these are easy to get backwards and cause real incidents):
//
//   1. Content-hashed, immutable assets (Vite emits /assets/<name>-<hash>.js|css,
//      fonts, wasm) are served CACHE-FIRST. A content change yields a NEW URL, so
//      cache-first is always correct AND avoids re-downloading large immutable
//      bundles on every load. Serving these network-first (a common mistake) makes
//      every navigation slow for zero freshness benefit.
//
//   2. Only the navigation shell (index.html) is NETWORK-FIRST, so a deploy is
//      picked up immediately while still working offline via cache fallback. The
//      shell is what wires up auth/session handoff, so it must never be stale.
//
//   3. Route immutable assets by extension/path, not by a hard-coded filename.
//      Hashed names are unknowable here; matching a literal like "/main.js" would
//      silently never hit, leaving those assets on the wrong strategy.
//
// Bump CACHE_VERSION on any change to this file's strategy or to non-hashed
// precached assets.
const CACHE_PREFIX = '{{PROJECT_NAME}}-shell'
const CACHE_VERSION = 'v1'
const CACHE_NAME = `${CACHE_PREFIX}-${CACHE_VERSION}`
const NAVIGATION_FALLBACK_URL = '/index.html'

// Non-hashed, stable assets safe to precache by fixed name. Do NOT list
// content-hashed bundles here — their names change every build.
const CORE_ASSETS = [NAVIGATION_FALLBACK_URL, '/manifest.webmanifest', '/favicon.ico']

const isSameOrigin = (url) => url.origin === self.location.origin

// Do not intercept API / websocket traffic.
const isRuntimeRequest = (url) =>
  url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws') || url.pathname.startsWith('/runtime/')

// Build-time content-hashed (or stable binary) assets → immutable → cache-first.
const isImmutableAssetRequest = (request, url) =>
  ['script', 'style', 'worker', 'font'].includes(request.destination) ||
  url.pathname.startsWith('/assets/') ||
  /\.(js|css|wasm|woff2?|ttf)$/i.test(url.pathname)

// Other same-origin static (images, icons, manifest) → stale-while-revalidate.
const isStaticAssetRequest = (request, url) =>
  ['image', 'manifest'].includes(request.destination) || /\.(png|svg|ico|webp|gif|json)$/i.test(url.pathname)

const putIfCacheable = async (request, response) => {
  if (!response || !response.ok) return response
  const cache = await caches.open(CACHE_NAME)
  await cache.put(request, response.clone())
  return response
}

const networkFirstNavigationShell = async () => {
  const cache = await caches.open(CACHE_NAME)
  const shellRequest = new Request(NAVIGATION_FALLBACK_URL, { cache: 'no-store', credentials: 'same-origin' })
  try {
    const response = await fetch(shellRequest)
    await putIfCacheable(NAVIGATION_FALLBACK_URL, response)
    return response
  } catch (error) {
    const fallback = await cache.match(NAVIGATION_FALLBACK_URL)
    if (fallback) return fallback
    throw error
  }
}

const cacheFirstStaticAsset = async (request) => {
  const cache = await caches.open(CACHE_NAME)
  const cached = await cache.match(request)
  if (cached) return cached
  const response = await fetch(request)
  return putIfCacheable(request, response)
}

const staleWhileRevalidate = async (request) => {
  const cache = await caches.open(CACHE_NAME)
  const cached = await cache.match(request)
  const networkPromise = fetch(request)
    .then((response) => putIfCacheable(request, response))
    .catch(() => undefined)
  if (cached) {
    void networkPromise
    return cached
  }
  const networkResponse = await networkPromise
  return networkResponse ?? Response.error()
}

self.addEventListener('install', (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE_NAME)
      await Promise.allSettled(CORE_ASSETS.map((asset) => cache.add(asset)))
      await self.skipWaiting()
    })()
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys()
      await Promise.all(
        keys.filter((key) => key.startsWith(CACHE_PREFIX) && key !== CACHE_NAME).map((key) => caches.delete(key))
      )
      await self.clients.claim()
    })()
  )
})

self.addEventListener('fetch', (event) => {
  const { request } = event
  if (request.method !== 'GET') return

  const url = new URL(request.url)
  if (!isSameOrigin(url) || isRuntimeRequest(url)) return

  if (request.mode === 'navigate') {
    event.respondWith(networkFirstNavigationShell())
    return
  }
  if (isImmutableAssetRequest(request, url)) {
    event.respondWith(cacheFirstStaticAsset(request))
    return
  }
  if (isStaticAssetRequest(request, url)) {
    event.respondWith(staleWhileRevalidate(request))
  }
})
