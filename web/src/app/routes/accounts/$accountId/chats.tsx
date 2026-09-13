import { createFileRoute, Outlet } from '@tanstack/react-router'
import { ChatList } from '@/features/chats/chat-list'

export const Route = createFileRoute('/accounts/$accountId/chats')({ component: ChatsLayout })

function ChatsLayout() {
  const { accountId } = Route.useParams()
  return (
    <div className="grid h-full grid-cols-[minmax(16rem,22rem)_1fr]">
      <ChatList accountId={accountId} />
      <div className="min-h-0 min-w-0 border-l">
        <Outlet />
      </div>
    </div>
  )
}
