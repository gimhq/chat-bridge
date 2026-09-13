import { createFileRoute } from '@tanstack/react-router'
import { PersonsPage } from '@/features/persons/persons-page'

export const Route = createFileRoute('/persons/')({ component: PersonsPage })
