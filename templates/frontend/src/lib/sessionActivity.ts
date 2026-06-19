// Shared idle-activity marker used by the idle-logout guard.
//
// Lesson encoded: this timestamp MUST be cleared on logout. If a previous
// session's "last activity" value survives into the next login, the idle guard
// can read a stale, already-expired timestamp the moment the next session
// starts and log the user out immediately. Always pair writes here with a
// clearSessionActivity() call in your logout path.
export const SESSION_ACTIVITY_STORAGE_KEY = '{{PROJECT_NAME}}:last_activity_at'

export const markSessionActivity = (timestamp: number = Date.now()): void => {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(SESSION_ACTIVITY_STORAGE_KEY, String(timestamp))
  } catch {
    // Ignore storage failures (private mode / quota); idleness still tracked in-memory.
  }
}

export const readSessionActivity = (): number | null => {
  if (typeof window === 'undefined') return null
  const raw = Number(window.localStorage.getItem(SESSION_ACTIVITY_STORAGE_KEY) || 0)
  return Number.isFinite(raw) && raw > 0 ? raw : null
}

export const clearSessionActivity = (): void => {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.removeItem(SESSION_ACTIVITY_STORAGE_KEY)
  } catch {
    // Ignore storage failures.
  }
}
