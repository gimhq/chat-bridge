import { createFileRoute } from '@tanstack/react-router'
import { PersonDetail } from '@/features/persons/person-detail'

export const Route = createFileRoute('/persons/$personId')({ component: PersonRoute })

function PersonRoute() {
  const { personId } = Route.useParams()
  return <PersonDetail key={personId} personId={personId} />
}
