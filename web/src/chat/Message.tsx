import { memo } from 'react';
import { ActionIcon, Anchor, Badge, CopyButton, Group, Stack, Text, Tooltip } from '@mantine/core';
import { IconCheck, IconCopy, IconPaperclip } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import catalogSchema from '../lib/catalogSchema';
import type { ChatValue } from '../lib/catalog.gen';
import { Markdown } from '../ui/Markdown';
import s from '../shell.module.css';

type ChatMessage = ChatValue['messages'][number];

const failed = catalogSchema.final_states.filter((x) => x !== 'done');

// ReplyState tells that the reply to a message of the owner failed, and why.
function ReplyState({ reply }: { reply: NonNullable<ChatMessage['reply']> }) {
  const { t } = useTranslation();
  return (
    <Group gap={4} justify="flex-end">
      <Badge size="xs" color="red" variant="light" data-reply-state={reply.state}>{t('chat.reply_' + reply.state, { defaultValue: reply.state })}</Badge>
      {reply.error && <Text size="xs" c="dimmed" lineClamp={2}>{reply.error}</Text>}
    </Group>
  );
}

// Message is a message of the owner as a bubble with its files, or a line of the master in markdown with its tools.
export const Message = memo(function Message({ m }: { m: ChatMessage }) {
  const { t } = useTranslation();
  if (m.role === 'owner') {
    const files = (m.files ?? []).map((f) => f.name);
    return (
      <Stack gap={4} align="flex-end" data-message="owner">
        <div className={s.ownerBubble}>
          {files.length > 0 && <Group gap={4} mb={6}>{files.map((f) => <Badge key={f} size="sm" variant="white" radius="sm" leftSection={<IconPaperclip size={11} />}>{f}</Badge>)}</Group>}
          <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>{m.text}</Text>
        </div>
        {m.reply && failed.includes(m.reply.state) && <ReplyState reply={m.reply} />}
      </Stack>
    );
  }
  return (
    <div className={s.masterLine} data-message="master">
      <Markdown text={m.text} />
      <Group gap={4} className={s.lineTools}>
        <CopyButton value={m.text}>{({ copied, copy }) => (
          <Tooltip label={copied ? t('chat.copied') : t('chat.copy')}>
            <ActionIcon variant="subtle" color="gray" size="sm" onClick={copy} aria-label={t('chat.copy')}>{copied ? <IconCheck size={14} /> : <IconCopy size={14} />}</ActionIcon>
          </Tooltip>
        )}</CopyButton>
        {m.reply?.open && <Anchor size="xs" c="dimmed" href={'#/deck/process/' + m.reply.process} data-reply-open>{t('chat.process', { id: m.reply.process })}</Anchor>}
      </Group>
    </div>
  );
});
