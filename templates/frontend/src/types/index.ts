// Common types used across the application

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
