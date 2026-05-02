// Common UI-only types used across the application.
//
// Domain API contracts belong in generated protobuf types under:
//   src/types/protos
//
// Run `make proto-ts` from the project root after changing files in api/protos.

// API response types
export interface ApiResponse<T> {
  data: T
  meta?: PaginationMeta
}

export interface PaginationMeta {
  page: number
  limit: number
  total: number
  totalPages: number
}

export interface ApiError {
  message: string
  code?: string
  details?: Record<string, unknown>
}

// User types
export interface User {
  id: string
  email: string
  name: string
  createdAt: string
  updatedAt: string
}

// Common entity types
export interface BaseEntity {
  id: string
  createdAt: string
  updatedAt: string
}

// Form types
export type FormStatus = 'idle' | 'loading' | 'success' | 'error'

// Utility types
export type Nullable<T> = T | null
export type Optional<T> = T | undefined
