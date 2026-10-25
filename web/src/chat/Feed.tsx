import type { ReactNode } from 'react';
import { ScrollArea, Stack } from '@mantine/core';
import type { ChatValue } from '../lib/catalog.gen';
import { Message } from './Message';
import type { useStickToBottom } from './useStickToBottom';

// Feed is the messages of the topic, kept at the bottom while the owner reads there, then what waits under them;
// empty is shown in place of no messages.
export function Feed({ messages, stick, empty, children }: {
  messages: ChatValue['messages']; stick: ReturnType<typeof useStickToBottom>; empty: ReactNode; children?: ReactNode;
}) {
  const last = messages[messages.length - 1]?.id;
  return (
    <ScrollArea style={{ flex: 1, minHeight: 80 }} viewportRef={stick.viewport} data-feed
      onWheel={stick.hand} onTouchMove={stick.hand} onKeyDown={stick.hand} onPointerDown={stick.hand} onScrollPositionChange={stick.onScroll}>
      <Stack gap="md" pr="xs" py="xs" mih="100%" ref={stick.content}>
        {!messages.length && empty}
        {messages.map((m) => <Message key={m.id} m={m} latest={m.id === last} />)}
        {children}
      </Stack>
    </ScrollArea>
  );
}
