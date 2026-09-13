import { createFileRoute } from '@tanstack/react-router'
import { AccountLayout } from '@/features/accounts/account-layout'

export const Route = createFileRoute('/accounts/$accountId')({ component: AccountRoute })

function AccountRoute() {
  const { accountId } = Route.useParams()
  return <AccountLayout accountId={accountId} />
}
