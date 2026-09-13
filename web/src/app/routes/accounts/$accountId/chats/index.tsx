import { createFileRoute } from '@tanstack/react-router'
import { MessagesSquareIcon } from 'lucide-react'
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/shared/components/ui/empty'

export const Route = createFileRoute('/accounts/$accountId/chats/')({ component: NoChat })

function NoChat() {
  return (
    <Empty className="h-full">
      <EmptyHeader>
        <EmptyMedia variant="icon"><MessagesSquareIcon /></EmptyMedia>
        <EmptyTitle>选择一个会话</EmptyTitle>
        <EmptyDescription>从左侧列表打开会话查看消息。</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}
