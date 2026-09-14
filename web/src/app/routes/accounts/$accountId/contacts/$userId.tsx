import { createFileRoute } from '@tanstack/react-router'
import { ContactDetail } from '@/features/contacts/contact-detail'

export const Route = createFileRoute('/accounts/$accountId/contacts/$userId')({ component: ContactRoute })

function ContactRoute() {
  const { accountId, userId } = Route.useParams()
  return <ContactDetail key={`${accountId}/${userId}`} accountId={accountId} userId={userId} />
}
