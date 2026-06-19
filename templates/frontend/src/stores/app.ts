import { create } from 'zustand'
import { devtools, persist } from 'zustand/middleware'

import { clearSessionActivity } from '../lib/sessionActivity'

interface User {
  id: string
  email: string
  name: string
}

interface AppState {
  // Auth state
  user: User | null
  isAuthenticated: boolean

  // UI state
  sidebarOpen: boolean

  // Actions
  setUser: (user: User | null) => void
  logout: () => void
  toggleSidebar: () => void
  setSidebarOpen: (open: boolean) => void
}

export const useAppStore = create<AppState>()(
  devtools(
    persist(
      (set) => ({
        // Initial state
        user: null,
        isAuthenticated: false,
        sidebarOpen: true,

        // Actions
        setUser: (user) =>
          set(
            { user, isAuthenticated: !!user },
            false,
            'setUser'
          ),

        logout: () => {
          // Clear the idle-activity marker so the next login starts a fresh idle
          // window instead of inheriting this session's (possibly expired) one.
          clearSessionActivity()
          set(
            { user: null, isAuthenticated: false },
            false,
            'logout'
          )
        },

        toggleSidebar: () =>
          set(
            (state) => ({ sidebarOpen: !state.sidebarOpen }),
            false,
            'toggleSidebar'
          ),

        setSidebarOpen: (open) =>
          set({ sidebarOpen: open }, false, 'setSidebarOpen'),
      }),
      {
        name: '{{PROJECT_NAME}}-storage',
        partialize: (state) => ({
          sidebarOpen: state.sidebarOpen,
        }),
      }
    ),
    { name: '{{PROJECT_NAME}}' }
  )
)
