import { createRootRoute, Outlet } from '@tanstack/react-router'
import { AppShell } from '@/features/shell/app-shell'
import { TokenGate } from '@/features/shell/token-gate'
import { useToken } from '@/shared/lib/token'

function Root() {
  const token = useToken()
  if (!token)
    return <TokenGate />
  return (
    <AppShell>
      <Outlet />
    </AppShell>
  )
}

export const Route = createRootRoute({ component: Root })
