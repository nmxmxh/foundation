import { useState, useCallback } from 'react'

interface ApiError {
  message: string
  code?: string
  details?: Record<string, unknown>
}

interface ApiState<T> {
  data: T | null
  error: ApiError | null
  isLoading: boolean
}

interface UseApiOptions {
  onSuccess?: (data: unknown) => void
  onError?: (error: ApiError) => void
}

export function useApi<T>(options?: UseApiOptions) {
  const [state, setState] = useState<ApiState<T>>({
    data: null,
    error: null,
    isLoading: false,
  })

  const execute = useCallback(
    async (promise: Promise<T>) => {
      setState((prev) => ({ ...prev, isLoading: true, error: null }))

      try {
        const data = await promise
        setState({ data, error: null, isLoading: false })
        options?.onSuccess?.(data)
        return { data, error: null }
      } catch (err) {
        const error: ApiError = {
          message: err instanceof Error ? err.message : 'An error occurred',
          code: (err as { code?: string })?.code,
        }
        setState({ data: null, error, isLoading: false })
        options?.onError?.(error)
        return { data: null, error }
      }
    },
    [options]
  )

  const reset = useCallback(() => {
    setState({ data: null, error: null, isLoading: false })
  }, [])

  return {
    ...state,
    execute,
    reset,
  }
}

export function useApiMutation<TData, TVariables>(
  mutationFn: (variables: TVariables) => Promise<TData>,
  options?: UseApiOptions
) {
  const { execute, ...state } = useApi<TData>(options)

  const mutate = useCallback(
    (variables: TVariables) => execute(mutationFn(variables)),
    [execute, mutationFn]
  )

  return {
    ...state,
    mutate,
  }
}
