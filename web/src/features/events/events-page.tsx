import type { ApiEvent } from '@/shared/api/types'
import { PauseIcon, PlayIcon, Trash2Icon } from 'lucide-react'
import { useState } from 'react'
import { PageHeader } from '@/shared/components/page-header'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Input } from '@/shared/components/ui/input'
import { clearEventLog, useEventLog, useStreamConnected } from '@/shared/lib/event-log'
import { formatTime } from '@/shared/lib/format'
import { filterEvents } from './filter-events'

export function EventsPage() {
  const live = useEventLog()
  const connected = useStreamConnected()
  const [paused, setPaused] = useState<ApiEvent[]>()
  const [filter, setFilter] = useState('')
  const shown = filterEvents(paused ?? live, filter)
  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="事件"
        description={connected ? '实时事件流（本页打开期间收到的最近 500 条）。' : '事件流断开，正在重连…'}
        actions={(
          <>
            <Input value={filter} onChange={e => setFilter(e.target.value)} placeholder="按类型或账号筛选" aria-label="筛选事件" className="h-8 w-56" />
            <Button variant="outline" size="sm" onClick={() => setPaused(paused ? undefined : live)}>
              {paused ? <PlayIcon /> : <PauseIcon />}
              {paused ? '继续' : '暂停'}
            </Button>
            <Button variant="ghost" size="sm" onClick={clearEventLog}>
              <Trash2Icon />
              清空
            </Button>
          </>
        )}
      />
      <div className="min-h-0 flex-1 overflow-y-auto">
        {shown.length === 0 && <p className="p-6 text-sm text-muted-foreground">暂无事件</p>}
        <ul className="divide-y font-mono text-xs">
          {shown.map(e => (
            <li key={e.id}>
              <details className="px-6 py-2">
                <summary className="flex cursor-pointer items-center gap-3">
                  <span className="w-28 shrink-0 text-muted-foreground">{formatTime(e.timestamp)}</span>
                  <Badge variant="secondary" className="font-mono">{e.type}</Badge>
                  <span className="truncate text-muted-foreground">{e.account_id}</span>
                  <span className="ml-auto shrink-0 text-muted-foreground">
                    #
                    {e.id.replace(/^0+/, '')}
                  </span>
                </summary>
                <pre className="mt-2 overflow-x-auto rounded-md bg-muted p-3">{JSON.stringify(e.data, null, 2)}</pre>
              </details>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}
