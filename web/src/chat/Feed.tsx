import { ScrollArea, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import { Message } from './Message';
import type { useStickToBottom } from './useStickToBottom';

// Feed is the messages of the topic, kept at the bottom while the owner reads there.
export function Feed({ messages, stick }: { messages: ChatValue['messages']; stick: ReturnType<typeof useStickToBottom> }) {
  const { t } = useTranslation();
  return (
    <ScrollArea style={{ flex: 1, minHeight: 80 }} viewportRef={stick.viewport} data-feed
      onWheel={stick.hand} onTouchMove={stick.hand} onKeyDown={stick.hand} onPointerDown={stick.hand} onScrollPositionChange={stick.onScroll}>
      <Stack gap="md" pr="xs" py="xs" ref={stick.content}>
        {!messages.length && <Text size="sm" c="dimmed" ta="center" mt="xl">{t('chat.empty')}</Text>}
        {messages.map((m) => <Message key={m.id} m={m} />)}
      </Stack>
    </ScrollArea>
  );
}
