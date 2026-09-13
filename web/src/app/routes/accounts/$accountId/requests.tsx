import { createFileRoute } from '@tanstack/react-router'
import { AccountRequests } from '@/features/requests/requests-page'

export const Route = createFileRoute('/accounts/$accountId/requests')({ component: AccountRequestsRoute })

function AccountRequestsRoute() {
  const { accountId } = Route.useParams()
  return <AccountRequests accountId={accountId} />
}
