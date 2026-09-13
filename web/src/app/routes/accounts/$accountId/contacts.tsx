import { createFileRoute } from '@tanstack/react-router'
import { ContactsPage } from '@/features/contacts/contacts-page'

export const Route = createFileRoute('/accounts/$accountId/contacts')({ component: ContactsRoute })

function ContactsRoute() {
  const { accountId } = Route.useParams()
  return <ContactsPage accountId={accountId} />
}
