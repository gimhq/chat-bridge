import { createFileRoute } from '@tanstack/react-router'
import { ChatView } from '@/features/chats/chat-view'

export const Route = createFileRoute('/accounts/$accountId/chats/$chatId')({ component: ChatRoute })

function ChatRoute() {
  const { accountId, chatId } = Route.useParams()
  return <ChatView key={chatId} accountId={accountId} chatId={chatId} />
}
