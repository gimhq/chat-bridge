import { QueryClient } from '@tanstack/react-query'
import { ApiError } from './http'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      refetchOnWindowFocus: false,
      // Client errors (not found, not ready, unauthorized) will not heal by retrying.
      retry: (count, err) => !(err instanceof ApiError && err.status < 500) && count < 1,
    },
  },
})
