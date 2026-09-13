import { Link } from '@tanstack/react-router'
import { BellOffIcon, SearchIcon, UsersIcon } from 'lucide-react'
import { useState } from 'react'
import { useAccount, useChats } from '@/shared/api/queries'
import { Avatar, AvatarFallback } from '@/shared/components/ui/avatar'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Input } from '@/shared/components/ui/input'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/shared/components/ui/tabs'
import { contentPreview, displayChat, formatTime } from '@/shared/lib/format'
import { cn } from '@/shared/lib/utils'
import { initials } from './chat-utils'
import { CreateGroupDialog } from './create-group-dialog'
import { SearchDialog } from './search-dialog'

export function ChatList({ accountId }: { accountId: string }) {
  const [archived, setArchived] = useState(false)
  const [filter, setFilter] = useState('')
  const account = useAccount(accountId)
  const chats = useChats(accountId, archived)
  const caps = account.data?.capabilities ?? []
  const all = chats.data?.pages.flatMap(p => p.chats) ?? []
  const q = filter.trim().toLowerCase()
  const list = q ? all.filter(c => displayChat(c).toLowerCase().includes(q) || c.id.toLowerCase().includes(q)) : all

  return (
    <div className="flex min-h-0 flex-col">
      <div className="flex flex-col gap-2 border-b p-3">
        <div className="flex items-center gap-2">
          <Input value={filter} onChange={e => setFilter(e.target.value)} placeholder="筛选会话" aria-label="筛选会话" className="h-8" />
          <SearchDialog accountId={accountId} trigger={<Button variant="outline" size="icon-sm" aria-label="搜索消息"><SearchIcon /></Button>} />
          {caps.includes('chat.create') && <CreateGroupDialog accountId={accountId} />}
        </div>
        <Tabs value={archived ? 'archived' : 'active'} onValueChange={v => setArchived(v === 'archived')}>
          <TabsList className="w-full">
            <TabsTrigger value="active">会话</TabsTrigger>
            <TabsTrigger value="archived">已归档</TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {chats.isLoading && <div className="flex flex-col gap-2 p-3">{[0, 1, 2, 3].map(i => <Skeleton key={i} className="h-12" />)}</div>}
        {chats.isSuccess && list.length === 0 && <p className="p-6 text-center text-sm text-muted-foreground">没有会话</p>}
        <ul>
          {list.map(c => (
            <li key={c.id}>
              <Link
                to="/accounts/$accountId/chats/$chatId"
                params={{ accountId, chatId: c.id }}
                className="flex items-center gap-3 px-3 py-2.5 transition-colors hover:bg-accent"
                activeProps={{ className: 'bg-accent' }}
              >
                <Avatar>
                  <AvatarFallback>{c.kind === 'group' ? <UsersIcon className="size-4" /> : initials(displayChat(c))}</AvatarFallback>
                </Avatar>
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline justify-between gap-2">
                    <span className={cn('truncate text-sm', c.unread_count > 0 && 'font-semibold')}>{displayChat(c)}</span>
                    <span className="shrink-0 text-xs text-muted-foreground">{formatTime(c.last_message_at)}</span>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <span className="truncate text-xs text-muted-foreground">
                      {c.last_message ? contentPreview(c.last_message.content) : ' '}
                    </span>
                    {c.muted && <BellOffIcon className="size-3 shrink-0 text-muted-foreground" aria-label="已静音" />}
                    {c.unread_count > 0 && <Badge className="ml-auto h-4 min-w-4 px-1 text-[10px]">{c.unread_count}</Badge>}
                  </div>
                </div>
              </Link>
            </li>
          ))}
        </ul>
        {chats.hasNextPage && (
          <div className="p-3">
            <Button variant="ghost" size="sm" className="w-full" disabled={chats.isFetchingNextPage} onClick={() => void chats.fetchNextPage()}>加载更多</Button>
          </div>
        )}
      </div>
    </div>
  )
}
