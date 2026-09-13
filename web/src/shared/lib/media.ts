import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo } from 'react'
import { apiBlob, seg } from './http'

/** Loads an attachment with the bearer token and exposes it as an object URL. */
export function useMediaUrl(mediaId: string | undefined, enabled = true) {
  const q = useQuery({
    queryKey: ['media', mediaId],
    queryFn: ({ signal }) => apiBlob(`/media/${seg(mediaId!)}`, signal),
    enabled: Boolean(mediaId) && enabled,
    staleTime: Number.POSITIVE_INFINITY,
    gcTime: 10 * 60_000,
    retry: false,
  })
  const url = useMemo(() => (q.data ? URL.createObjectURL(q.data) : undefined), [q.data])
  useEffect(() => () => {
    if (url)
      URL.revokeObjectURL(url)
  }, [url])
  return { url, isLoading: q.isLoading, error: q.error }
}

/** Downloads an attachment through the token-bearing client. */
export async function downloadMedia(mediaId: string, fileName: string) {
  const blob = await apiBlob(`/media/${seg(mediaId)}`)
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = fileName
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 10_000)
}
