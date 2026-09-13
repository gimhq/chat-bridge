import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { ActivityIcon, BellRingIcon, ContactRoundIcon, LogOutIcon, MessagesSquareIcon, ServerIcon, UsersIcon } from 'lucide-react'
import { useLiveEvents } from '@/features/live/use-live-events'
import { useAccounts, useAllRequests } from '@/shared/api/queries'
import { ModeToggle } from '@/shared/components/mode-toggle'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/shared/components/ui/tooltip'
import { useStreamConnected } from '@/shared/lib/event-log'
import { clearToken } from '@/shared/lib/token'
import { cn } from '@/shared/lib/utils'

const navLink = 'flex items-center gap-2 rounded-lg px-2.5 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground'
const navActive = 'bg-accent font-medium text-accent-foreground'

export function AppShell({ children }: { children: ReactNode }) {
  useLiveEvents()
  const connected = useStreamConnected()
  const accounts = useAccounts()
  const pending = useAllRequests((accounts.data ?? []).map(a => a.id), 'pending')

  return (
    <div className="flex h-svh bg-background text-foreground">
      <aside className="flex w-56 shrink-0 flex-col border-r bg-sidebar p-3">
        <div className="flex items-center gap-2 px-2 pb-4 pt-1">
          <MessagesSquareIcon className="size-5" aria-hidden />
          <span className="font-heading font-semibold">chat-bridge</span>
        </div>
        <nav aria-label="主导航" className="flex flex-col gap-1">
          <Link to="/accounts" className={navLink} activeProps={{ className: navActive }}>
            <UsersIcon className="size-4" aria-hidden />
            账号
          </Link>
          <Link to="/persons" className={navLink} activeProps={{ className: navActive }}>
            <ContactRoundIcon className="size-4" aria-hidden />
            人
          </Link>
          <Link to="/requests" className={navLink} activeProps={{ className: navActive }}>
            <BellRingIcon className="size-4" aria-hidden />
            请求
            {pending.data.length > 0 && <Badge className="ml-auto" variant="destructive">{pending.data.length}</Badge>}
          </Link>
          <Link to="/events" className={navLink} activeProps={{ className: navActive }}>
            <ActivityIcon className="size-4" aria-hidden />
            事件
          </Link>
          <Link to="/system" className={navLink} activeProps={{ className: navActive }}>
            <ServerIcon className="size-4" aria-hidden />
            系统
          </Link>
        </nav>
        <div className="mt-auto flex items-center gap-1 border-t pt-3">
          <Tooltip>
            <TooltipTrigger render={<span className="flex items-center gap-1.5 px-2 text-xs text-muted-foreground" />}>
              <span className={cn('size-2 rounded-full', connected ? 'bg-success' : 'bg-warning')} aria-hidden />
              {connected ? '实时' : '重连中'}
            </TooltipTrigger>
            <TooltipContent>{connected ? '事件流已连接' : '事件流断开，正在重连'}</TooltipContent>
          </Tooltip>
          <ModeToggle className="ml-auto" />
          <Tooltip>
            <TooltipTrigger render={<Button variant="ghost" size="icon" aria-label="退出" onClick={clearToken} />}>
              <LogOutIcon />
            </TooltipTrigger>
            <TooltipContent>清除令牌</TooltipContent>
          </Tooltip>
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-hidden">{children}</main>
    </div>
  )
}
