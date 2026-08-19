import { useEffect, useRef } from 'react'

import {
  SESSION_ACTIVITY_STORAGE_KEY,
  clearSessionActivity,
  markSessionActivity,
  readSessionActivity,
} from '../lib/sessionActivity'

const ACTIVITY_EVENTS: (keyof WindowEventMap)[] = [
  'mousemove',
  'mousedown',
  'keydown',
  'touchstart',
  'wheel',
  'focus',
]

export type UseIdleLogoutOptions = {
  // Whether a session is currently active. Keep this tied to session PRESENCE
  // (e.g. isAuthenticated), NOT to the token value — see the note below.
  enabled: boolean
  // Idle window before logout fires, in milliseconds.
  idleTimeoutMs: number
  // How often to re-check idleness, in milliseconds.
  checkIntervalMs?: number
  // Throttle for persisting activity to storage, in milliseconds.
  persistThrottleMs?: number
  // Invoked when the idle window elapses. Wire this to your logout action.
  onIdle: () => void
}

/**
 * Idle-logout guard.
 *
 * Two lessons are baked in:
 *
 *  1. Key on session PRESENCE (`enabled`/isAuthenticated), never on the token.
 *     A token rotation (e.g. a context switch that mints a new access token)
 *     would otherwise re-run this effect and re-evaluate the idle window against
 *     a stale persisted timestamp — logging the user out the instant they switch
 *     context, with no error to show for it. The running interval already tracks
 *     idleness continuously, so we only (re)initialize when a session begins.
 *
 *  2. The persisted activity marker is cleared on logout (see clearSessionActivity)
 *     so a prior session's idle state cannot bleed into the next login.
 *
 *  3. The session ends in EVERY tab, not just the one that timed out. Activity
 *     is shared through localStorage, so any active tab keeps the others alive —
 *     but the reverse did not hold: a tab that idled out logged itself out while
 *     its siblings stayed authenticated, which is precisely the state idle logout
 *     exists to prevent (the user has walked away, and a live session remains on
 *     screen). The `storage` event carries the cleared marker to the other tabs,
 *     which then end their own sessions. Storage events do not fire in the tab
 *     that wrote them, so this cannot loop.
 */
export const useIdleLogout = ({
  enabled,
  idleTimeoutMs,
  checkIntervalMs = 30_000,
  persistThrottleMs = 5_000,
  onIdle,
}: UseIdleLogoutOptions): void => {
  const lastActivityRef = useRef<number>(Date.now())
  const lastPersistRef = useRef<number>(0)
  // Keep the latest onIdle without making it an effect dependency (which would
  // tear down/re-init the guard on every render that passes a new callback).
  const onIdleRef = useRef(onIdle)
  onIdleRef.current = onIdle

  useEffect(() => {
    if (!enabled) return

    const persisted = readSessionActivity()
    const initial = persisted ?? Date.now()
    if (Date.now() - initial >= idleTimeoutMs) {
      clearSessionActivity()
      onIdleRef.current()
      return
    }

    lastActivityRef.current = initial
    markSessionActivity(initial)

    const persistActivity = (timestamp: number) => {
      if (timestamp - lastPersistRef.current < persistThrottleMs) return
      lastPersistRef.current = timestamp
      markSessionActivity(timestamp)
    }

    const markActivity = () => {
      const now = Date.now()
      lastActivityRef.current = now
      persistActivity(now)
    }

    const handleVisibility = () => {
      if (document.visibilityState === 'visible') markActivity()
    }

    // Another tab ended the session: it cleared the marker (idle timeout, or an
    // explicit logout, both of which call clearSessionActivity). `newValue` of
    // null is the removal; a null `key` is a whole-storage clear, which ends the
    // session for the same reason.
    const handleStorage = (event: StorageEvent) => {
      if (event.key !== null && event.key !== SESSION_ACTIVITY_STORAGE_KEY) return
      if (event.newValue !== null) return
      onIdleRef.current()
    }

    ACTIVITY_EVENTS.forEach((event) => window.addEventListener(event, markActivity, { passive: true }))
    document.addEventListener('visibilitychange', handleVisibility)
    window.addEventListener('storage', handleStorage)

    const intervalId = window.setInterval(() => {
      const persistedNow = readSessionActivity()
      if (persistedNow) lastActivityRef.current = persistedNow
      if (Date.now() - lastActivityRef.current >= idleTimeoutMs) {
        clearSessionActivity()
        onIdleRef.current()
      }
    }, checkIntervalMs)

    return () => {
      window.clearInterval(intervalId)
      ACTIVITY_EVENTS.forEach((event) => window.removeEventListener(event, markActivity))
      document.removeEventListener('visibilitychange', handleVisibility)
      window.removeEventListener('storage', handleStorage)
    }
    // NOTE: deliberately keyed on session presence + config, NOT on the token.
  }, [enabled, idleTimeoutMs, checkIntervalMs, persistThrottleMs])
}
