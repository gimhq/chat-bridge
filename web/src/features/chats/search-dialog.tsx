import type { ReactElement } from 'react'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useSearchMessages } from '@/shared/api/queries'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from '@/shared/components/ui/dialog'
import { Input } from '@/shared/components/ui/input'
import { contentPreview, formatTime, senderName } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'

export function SearchDialog({ accountId, chatId, trigger }: { accountId: string, chatId?: string, trigger: ReactElement }) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const search = useSearchMessages(accountId, open ? q : '', chatId)
  const results = search.data?.messages ?? []
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={trigger} />
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{chatId ? '在会话中搜索' : '搜索消息'}</DialogTitle>
        </DialogHeader>
        <Input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="关键词（按空格分隔多个词）" aria-label="搜索关键词" />
        <div className="max-h-96 overflow-y-auto">
          {search.error && <p className="text-sm text-destructive">{errorMessage(search.error)}</p>}
          {q.trim() !== '' && search.isSuccess && results.length === 0 && <p className="py-6 text-center text-sm text-muted-foreground">没有匹配的消息</p>}
          <ul className="divide-y">
            {results.map(m => (
              <li key={`${m.chat_id}/${m.id}`}>
                <Link
                  to="/accounts/$accountId/chats/$chatId"
                  params={{ accountId, chatId: m.chat_id }}
                  onClick={() => setOpen(false)}
                  className="block px-1 py-2 hover:bg-accent"
                >
                  <div className="flex justify-between gap-2 text-xs text-muted-foreground">
                    <span className="truncate">
                      {senderName(m)}
                      {' '}
                      ·
                      {' '}
                      {m.chat_id}
                    </span>
                    <span className="shrink-0">{formatTime(m.timestamp)}</span>
                  </div>
                  <p className="line-clamp-2 text-sm">{contentPreview(m.content)}</p>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      </DialogContent>
    </Dialog>
  )
}
