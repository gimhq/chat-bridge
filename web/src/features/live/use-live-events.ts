import type { BridgeStatus, ChatRequest } from '@/shared/api/types'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useEffect } from 'react'
import { toast } from 'sonner'
import { invalidationsFor, qk, useAnswerRequest } from '@/shared/api/queries'
import { recordEvent, setStreamConnected } from '@/shared/lib/event-log'
import { requestKindLabels } from '@/shared/lib/format'
import { api, errorMessage } from '@/shared/lib/http'
import { streamEvents } from '@/shared/lib/sse'
import { useToken } from '@/shared/lib/token'

/** Follows the event stream: logs events, refreshes affected queries, surfaces new requests. */
export function useLiveEvents() {
  const qc = useQueryClient()
  const token = useToken()
  const navigate = useNavigate()
  const { mutate: answerRequest } = useAnswerRequest()

  useEffect(() => {
    if (!token)
      return
    let stop: (() => void) | undefined
    let cancelled = false
    // Start at the current cursor: without one the server replays retained history, which would
    // re-toast old requests on every page load.
    void api<BridgeStatus>('/status').then(s => s.events_cursor, () => undefined).then((since) => {
      if (cancelled)
        return
      stop = streamEvents({
        since,
        onStatus: setStreamConnected,
        onEvent: (e) => {
          recordEvent(e)
          for (const key of invalidationsFor(e))
            void qc.invalidateQueries({ queryKey: key })
          if (e.type === 'account.login_step' && e.account_id)
            qc.setQueryData(qk.login(e.account_id), e.data)
          if (e.type === 'request.new') {
            const r = e.data as ChatRequest
            const who = r.from?.name || r.from?.id || ''
            const title = `${requestKindLabels[r.kind]}${who ? `：${who}` : ''}`
            const description = [r.account_id, r.chat?.name].filter(Boolean).join(' · ')
            if (r.kind === 'call') {
              toast.warning(title, {
                description,
                duration: 30_000,
                action: {
                  label: '忽略',
                  onClick: () => answerRequest({ account: r.account_id, id: r.id, action: 'ignore' }, { onError: err => toast.error(errorMessage(err)) }),
                },
              })
            }
            else {
              toast.info(title, { description, action: { label: '查看', onClick: () => void navigate({ to: '/requests' }) } })
            }
          }
        },
      })
    })
    return () => {
      cancelled = true
      stop?.()
    }
  }, [qc, token, navigate, answerRequest])
}
