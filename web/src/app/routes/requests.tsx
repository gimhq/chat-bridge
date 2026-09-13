import { createFileRoute } from '@tanstack/react-router'
import { AllRequestsPage } from '@/features/requests/requests-page'

export const Route = createFileRoute('/requests')({ component: AllRequestsPage })
