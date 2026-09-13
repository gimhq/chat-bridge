import type { Account } from '@/shared/api/types'
import { useNavigate } from '@tanstack/react-router'
import { LogOutIcon, RefreshCwIcon, Trash2Icon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useAccount, useAccountAction, useDeleteAccount } from '@/shared/api/queries'
import { Alert, AlertDescription, AlertTitle } from '@/shared/components/ui/alert'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from '@/shared/components/ui/alert-dialog'
import { Badge } from '@/shared/components/ui/badge'
import { Button } from '@/shared/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/shared/components/ui/card'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { formatTime } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { LoginPanel } from './login-panel'
import { SelfEditor } from './self-editor'

export function AccountOverview({ accountId }: { accountId: string }) {
  const account = useAccount(accountId)
  if (account.isLoading)
    return <div className="p-6"><Skeleton className="h-48 w-full" /></div>
  const a = account.data
  if (!a)
    return null
  const needsLogin = a.status === 'unpaired' || a.status === 'logging_in'
  return (
    <div className="grid gap-4 p-6 xl:grid-cols-2">
      {a.error && (
        <Alert variant="destructive" className="xl:col-span-2">
          <AlertTitle>{a.error.code}</AlertTitle>
          <AlertDescription>{a.error.message}</AlertDescription>
        </Alert>
      )}
      {needsLogin && <LoginPanel account={a} />}
      <StatusCard account={a} />
      {a.status === 'connected' && <SelfEditor account={a} />}
    </div>
  )
}

function Row({ label, children }: { label: string, children: React.ReactNode }) {
  return (
    <div className="flex justify-between gap-4 py-1.5 text-sm">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 truncate text-right">{children}</dd>
    </div>
  )
}

function StatusCard({ account: a }: { account: Account }) {
  const action = useAccountAction(a.id)
  const del = useDeleteAccount()
  const navigate = useNavigate()
  const [confirm, setConfirm] = useState<'logout' | 'delete' | null>(null)
  const run = (kind: 'logout' | 'reconnect') =>
    action.mutate(kind, { onSuccess: () => toast.success(kind === 'logout' ? '已登出' : '已请求重连'), onError: e => toast.error(errorMessage(e)) })

  return (
    <Card>
      <CardHeader>
        <CardTitle>状态</CardTitle>
        <CardDescription>
          {a.platform}
          {a.adapter ? ` · 实例 ${a.adapter}` : ''}
        </CardDescription>
        <CardAction className="flex gap-1">
          <Button variant="outline" size="sm" disabled={action.isPending || a.status === 'unpaired'} onClick={() => run('reconnect')}>
            <RefreshCwIcon />
            重连
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <dl className="divide-y">
          <Row label="连接于">{formatTime(a.connected_at) || '—'}</Row>
          <Row label="登录方式">{a.login ? `${a.login.flow}${a.login.identifier ? ` · ${a.login.identifier}` : ''}` : '—'}</Row>
          <Row label="会话 / 消息">{a.stats ? `${a.stats.chats} / ${a.stats.messages}` : '—'}</Row>
          <Row label="待处理请求">{a.stats?.requests_pending ?? 0}</Row>
          <Row label="最近收到">{formatTime(a.stats?.last_inbound_at) || '—'}</Row>
          <Row label="自身 ID">{a.self?.id ?? '—'}</Row>
          <Row label="号码 / 句柄">{a.self?.phone || a.self?.handle || '—'}</Row>
        </dl>
        <div className="flex flex-wrap gap-1">
          {a.capabilities.map(c => <Badge key={c} variant="secondary">{c}</Badge>)}
        </div>
        <div className="flex flex-wrap gap-2 border-t pt-4">
          <AlertDialog open={confirm !== null} onOpenChange={o => !o && setConfirm(null)}>
            <AlertDialogTrigger render={<Button variant="outline" size="sm" disabled={a.status === 'unpaired'} onClick={() => setConfirm('logout')} />}>
              <LogOutIcon />
              登出
            </AlertDialogTrigger>
            <Button variant="destructive" size="sm" onClick={() => setConfirm('delete')}>
              <Trash2Icon />
              删除账号
            </Button>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{confirm === 'delete' ? `删除账号 ${a.id}？` : `登出 ${a.id}？`}</AlertDialogTitle>
                <AlertDialogDescription>
                  {confirm === 'delete' ? '会登出并删除本地会话、消息和媒体，无法恢复。' : '平台会话被注销，之后需要重新登录；本地消息保留。'}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>取消</AlertDialogCancel>
                <AlertDialogAction
                  variant="destructive"
                  onClick={() => {
                    if (confirm === 'logout') {
                      run('logout')
                    }
                    else {
                      del.mutate(a.id, {
                        onSuccess: () => {
                          toast.success(`已删除 ${a.id}`)
                          void navigate({ to: '/accounts' })
                        },
                        onError: e => toast.error(errorMessage(e)),
                      })
                    }
                    setConfirm(null)
                  }}
                >
                  确认
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </CardContent>
    </Card>
  )
}
