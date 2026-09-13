import { Link, Outlet } from '@tanstack/react-router'
import { useAccount } from '@/shared/api/queries'
import { PageHeader } from '@/shared/components/page-header'
import { StatusBadge } from '@/shared/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/shared/components/ui/alert'
import { displayContact } from '@/shared/lib/format'
import { errorMessage } from '@/shared/lib/http'

const tab = 'border-b-2 border-transparent px-3 py-2 text-sm text-muted-foreground transition-colors hover:text-foreground'
const tabActive = 'border-primary font-medium text-foreground'

export function AccountLayout({ accountId }: { accountId: string }) {
  const account = useAccount(accountId)
  const a = account.data
  const pending = a?.stats?.requests_pending ?? 0
  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={(
          <span className="flex items-center gap-2">
            {accountId}
            {a && <StatusBadge status={a.status} />}
          </span>
        )}
        description={a ? `${a.platform} · ${displayContact(a.self) || '未登录'}` : undefined}
      />
      <nav aria-label="账号页面" className="flex gap-1 border-b px-4">
        <Link to="/accounts/$accountId" params={{ accountId }} activeOptions={{ exact: true }} className={tab} activeProps={{ className: tabActive }}>概览</Link>
        <Link to="/accounts/$accountId/chats" params={{ accountId }} className={tab} activeProps={{ className: tabActive }}>会话</Link>
        <Link to="/accounts/$accountId/contacts" params={{ accountId }} className={tab} activeProps={{ className: tabActive }}>联系人</Link>
        <Link to="/accounts/$accountId/requests" params={{ accountId }} className={tab} activeProps={{ className: tabActive }}>
          请求
          {pending > 0 ? `（${pending}）` : ''}
        </Link>
      </nav>
      {account.error
        ? (
            <div className="p-6">
              <Alert variant="destructive">
                <AlertTitle>无法加载账号</AlertTitle>
                <AlertDescription>{errorMessage(account.error)}</AlertDescription>
              </Alert>
            </div>
          )
        : <div className="min-h-0 flex-1 overflow-auto"><Outlet /></div>}
    </div>
  )
}
