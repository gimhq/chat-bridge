import { Link } from '@tanstack/react-router'
import { UsersIcon } from 'lucide-react'
import { useAccounts } from '@/shared/api/queries'
import { PageHeader } from '@/shared/components/page-header'
import { StatusBadge } from '@/shared/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/shared/components/ui/alert'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/shared/components/ui/empty'
import { Skeleton } from '@/shared/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/components/ui/table'
import { displayContact, formatTime } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'
import { CreateAccountDialog } from './create-account-dialog'

export function AccountsPage() {
  const accounts = useAccounts()
  const list = accounts.data ?? []
  return (
    <div className="flex h-full flex-col overflow-auto">
      <PageHeader title="账号" description="每个账号是一个平台上的登录身份。" actions={<CreateAccountDialog />} />
      <div className="p-6">
        {accounts.error && (
          <Alert variant="destructive">
            <AlertTitle>加载失败</AlertTitle>
            <AlertDescription>{errorMessage(accounts.error)}</AlertDescription>
          </Alert>
        )}
        {accounts.isLoading && <Skeleton className="h-40 w-full" />}
        {accounts.isSuccess && list.length === 0 && (
          <Empty className="border">
            <EmptyHeader>
              <EmptyMedia variant="icon"><UsersIcon /></EmptyMedia>
              <EmptyTitle>还没有账号</EmptyTitle>
              <EmptyDescription>新建账号后按提示登录（扫码、验证码或密码）。</EmptyDescription>
            </EmptyHeader>
            <EmptyContent><CreateAccountDialog /></EmptyContent>
          </Empty>
        )}
        {list.length > 0 && (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>账号</TableHead>
                  <TableHead>平台</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>身份</TableHead>
                  <TableHead>实例</TableHead>
                  <TableHead>创建</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {list.map(a => (
                  <TableRow key={a.id}>
                    <TableCell className="font-medium">
                      <Link to="/accounts/$accountId" params={{ accountId: a.id }} className="underline-offset-4 hover:underline">{a.id}</Link>
                    </TableCell>
                    <TableCell>{a.platform}</TableCell>
                    <TableCell><StatusBadge status={a.status} /></TableCell>
                    <TableCell className="text-muted-foreground">{displayContact(a.self) || '—'}</TableCell>
                    <TableCell className="text-muted-foreground">{a.adapter || '—'}</TableCell>
                    <TableCell className="text-muted-foreground">{formatTime(a.created_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </div>
  )
}
