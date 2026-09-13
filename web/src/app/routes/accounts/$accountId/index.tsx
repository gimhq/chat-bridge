import { createFileRoute } from '@tanstack/react-router'
import { AccountOverview } from '@/features/accounts/account-overview'

export const Route = createFileRoute('/accounts/$accountId/')({ component: OverviewRoute })

function OverviewRoute() {
  const { accountId } = Route.useParams()
  return <AccountOverview accountId={accountId} />
}
