import type { ChatRequest, RequestAction, RequestState } from '@/shared/api/types'
import { BellOffIcon, BellRingIcon, CheckIcon, PhoneIncomingIcon, UserPlusIcon, UsersIcon, VideoIcon, XIcon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useAccounts, useAllRequests, useAnswerRequest, useRequests } from '@/shared/api/queries'
import { PageHeader } from '@/shared/components/page-header'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/shared/components/ui/empty'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { Spinner } from '@/shared/components/ui/spinner'
import { Tabs, TabsList, TabsTrigger } from '@/shared/components/ui/tabs'
import { formatTime, requestKindLabels, requestStateLabels } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'

function StateTabs({ value, onChange }: { value: RequestState | undefined, onChange: (v: RequestState | undefined) => void }) {
  return (
    <Tabs value={value ?? 'all'} onValueChange={v => onChange(v === 'all' ? undefined : v as RequestState)}>
      <TabsList>
        <TabsTrigger value="pending">待处理</TabsTrigger>
        <TabsTrigger value="all">全部</TabsTrigger>
      </TabsList>
    </Tabs>
  )
}

export function AllRequestsPage() {
  const [state, setState] = useState<RequestState | undefined>('pending')
  const accounts = useAccounts()
  const requests = useAllRequests((accounts.data ?? []).map(a => a.id), state)
  return (
    <div className="flex h-full flex-col overflow-auto">
      <PageHeader title="请求" description="邀请、入群申请和来电。忽略来电不会挂断，手机等其他设备仍可接听。" actions={<StateTabs value={state} onChange={setState} />} />
      <div className="p-6">
        <RequestList requests={requests.data} loading={accounts.isLoading || requests.isLoading} showAccount />
      </div>
    </div>
  )
}

export function AccountRequests({ accountId }: { accountId: string }) {
  const [state, setState] = useState<RequestState | undefined>('pending')
  const requests = useRequests(accountId, state)
  return (
    <div className="flex flex-col gap-4 p-6">
      <StateTabs value={state} onChange={setState} />
      <RequestList requests={requests.data ?? []} loading={requests.isLoading} />
    </div>
  )
}

export function RequestList({ requests, loading, showAccount }: { requests: ChatRequest[], loading: boolean, showAccount?: boolean }) {
  if (loading)
    return <Skeleton className="h-32" />
  if (requests.length === 0) {
    return (
      <Empty className="border">
        <EmptyHeader>
          <EmptyMedia variant="icon"><BellRingIcon /></EmptyMedia>
          <EmptyTitle>没有请求</EmptyTitle>
          <EmptyDescription>收到邀请或来电时会出现在这里，并弹出提醒。</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <ul className="flex flex-col gap-2">
      {requests.map(r => <RequestRow key={`${r.account_id}/${r.id}`} request={r} showAccount={showAccount} />)}
    </ul>
  )
}

function KindIcon({ r }: { r: ChatRequest }) {
  const cls = 'size-5 text-muted-foreground'
  if (r.kind === 'call')
    return r.call?.kind === 'video' ? <VideoIcon className={cls} aria-hidden /> : <PhoneIncomingIcon className={cls} aria-hidden />
  if (r.kind === 'join_request')
    return <UserPlusIcon className={cls} aria-hidden />
  return <UsersIcon className={cls} aria-hidden />
}

function RequestRow({ request: r, showAccount }: { request: ChatRequest, showAccount?: boolean }) {
  const answer = useAnswerRequest()
  const who = r.from?.name || r.from?.id
  const act = (action: RequestAction) =>
    answer.mutate({ account: r.account_id, id: r.id, action }, {
      onSuccess: () => toast.success(({ accept: '已接受', reject: '已拒绝', ignore: '已忽略' } as const)[action]),
      onError: e => toast.error(errorMessage(e)),
    })
  return (
    <li className="flex items-center gap-3 rounded-lg border p-3">
      <KindIcon r={r} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">
            {r.kind === 'call' ? `${r.call?.kind === 'video' ? '视频' : '语音'}来电` : requestKindLabels[r.kind]}
          </span>
          <Badge variant={r.state === 'pending' ? 'default' : 'secondary'}>{requestStateLabels[r.state]}</Badge>
          {showAccount && <Badge variant="outline">{r.account_id}</Badge>}
        </div>
        <p className="truncate text-sm text-muted-foreground">
          {who && `来自 ${who}`}
          {r.chat?.name || (r.kind !== 'call' && r.chat?.id) ? ` · ${r.chat?.name || r.chat?.id}` : ''}
          {r.message ? ` · “${r.message}”` : ''}
        </p>
        <p className="text-xs text-muted-foreground">
          {formatTime(r.created_at)}
          {r.state === 'pending' && r.expires_at ? ` · ${formatTime(r.expires_at)} 过期` : ''}
          {r.answered_at ? ` · 处理于 ${formatTime(r.answered_at)}` : ''}
        </p>
      </div>
      <div className="flex shrink-0 gap-2">
        {r.actions.includes('accept') && (
          <Button size="sm" disabled={answer.isPending} onClick={() => act('accept')}>
            {answer.isPending && answer.variables?.action === 'accept' ? <Spinner /> : <CheckIcon />}
            接受
          </Button>
        )}
        {r.actions.includes('reject') && (
          <Button size="sm" variant={r.kind === 'call' ? 'destructive' : 'outline'} disabled={answer.isPending} onClick={() => act('reject')}>
            {answer.isPending && answer.variables?.action === 'reject' ? <Spinner /> : <XIcon />}
            拒绝
          </Button>
        )}
        {r.actions.includes('ignore') && (
          <Button size="sm" variant="ghost" disabled={answer.isPending} onClick={() => act('ignore')}>
            {answer.isPending && answer.variables?.action === 'ignore' ? <Spinner /> : <BellOffIcon />}
            忽略
          </Button>
        )}
      </div>
    </li>
  )
}
