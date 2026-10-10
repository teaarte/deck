import { memo, useState } from 'react';
import { ActionIcon, Badge, Button, CopyButton, Group, Stack, Text, Tooltip } from '@mantine/core';
import { IconCheck, IconCopy, IconPaperclip, IconTrash } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { call } from '../lib/api';
import catalogSchema from '../lib/catalogSchema';
import { notifyError } from '../lib/notify';
import type { ChatValue } from '../lib/catalog.gen';
import { ActionButton } from '../ui/actions/ActionButton';
import { DekiHead, useMascot } from '../ui/deki/Deki';
import { Markdown } from '../ui/Markdown';
import s from './chat.module.css';

type ChatMessage = ChatValue['messages'][number];

const failed = catalogSchema.final_states.filter((x) => x !== 'done');

type Cut = NonNullable<NonNullable<ChatMessage['reply']>['cut']>;

// Broken is what a reply that broke off did and left, each change with its undo, and the providers it can go again on.
function Broken({ cut, provider }: { cut: Cut; provider?: string }) {
  const { t } = useTranslation();
  const [undoing, setUndoing] = useState(false);
  const undoAll = async () => {
    setUndoing(true);
    try {
      for (const u of cut.undo) await call(u.action.command!, u.action.params ?? {});
    } catch (e) {
      notifyError(e);
    } finally {
      setUndoing(false);
    }
  };
  return (
    <Stack gap={6} className={s.cut} data-reply-cut>
      <Text size="xs" fw={600}>{t('chat.cut_by', { provider: provider || '?' })}</Text>
      {cut.did.length > 0 && (
        <Stack gap={2}>
          <Text size="xs" c="dimmed">{t('chat.cut_did')}</Text>
          {cut.did.map((d, i) => <Text key={i} size="xs" ff="monospace" lineClamp={1} title={d}>{d}</Text>)}
        </Stack>
      )}
      {cut.undo.length > 0 && (
        <Stack gap={4}>
          <Text size="xs" c="dimmed">{t('chat.cut_left')}</Text>
          {cut.undo.map((u, i) => (
            <Group key={i} gap={6} wrap="nowrap" justify="space-between">
              <Text size="xs" lineClamp={2}>{u.text}</Text>
              <ActionButton act={u.action} />
            </Group>
          ))}
          {cut.undo.length > 1 && <Button size="xs" variant="light" color="red" loading={undoing} onClick={() => void undoAll()} data-undo-all>{t('chat.undo_all')}</Button>}
        </Stack>
      )}
      {cut.retry.length > 0 && (
        <Group gap={4}>
          {cut.retry.map((r) => <ActionButton key={r.label} act={{ ...r, label: t('chat.retry_on', { provider: r.label }) }} />)}
        </Group>
      )}
    </Stack>
  );
}

// ReplyState tells that the reply to a message of the owner failed, and why, with what a broken one left.
function ReplyState({ reply }: { reply: NonNullable<ChatMessage['reply']> }) {
  const { t } = useTranslation();
  return (
    <Stack gap={4} align="flex-end">
      <Group gap={4} justify="flex-end">
        <Badge size="xs" color="red" variant="light" data-reply-state={reply.state}>{t('chat.reply_' + reply.state, { defaultValue: reply.state })}</Badge>
        {reply.error && <Text size="xs" c="dimmed" lineClamp={2}>{reply.error}</Text>}
      </Group>
      {reply.cut && <Broken cut={reply.cut} provider={reply.provider} />}
    </Stack>
  );
}

// Message is a message of the owner as a bubble with its files, or a line of the master in markdown with its tools and
// the face of Deki.
export const Message = memo(function Message({ m }: { m: ChatMessage }) {
  const { t } = useTranslation();
  const mascot = useMascot();
  if (m.role === 'owner') {
    const files = (m.files ?? []).map((f) => f.name);
    return (
      <Stack gap={4} align="flex-end" data-message="owner">
        <div className={s.ownerBubble}>
          {files.length > 0 && <Group gap={4} mb={6}>{files.map((f) => <Badge key={f} size="sm" variant="white" radius="sm" leftSection={<IconPaperclip size={11} />}>{f}</Badge>)}</Group>}
          <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>{m.text}</Text>
        </div>
        {m.reply && failed.includes(m.reply.state) && <ReplyState reply={m.reply} />}
        {!m.process && (
          <Tooltip label={t('chat.delete')}>
            <ActionIcon variant="subtle" color="gray" size="sm" aria-label={t('chat.delete')} data-message-delete={m.id}
              onClick={() => { call('deck.chat_message_delete', { message: m.id }).catch((e) => notifyError(e)); }}>
              <IconTrash size={14} />
            </ActionIcon>
          </Tooltip>
        )}
      </Stack>
    );
  }
  return (
    <div className={s.masterLine} data-message="master" data-avatar={mascot || undefined} tabIndex={-1}>
      {mascot && <span className={s.avatar}><DekiHead size={28} /></span>}
      <Markdown text={m.text} />
      <Group gap={4} className={s.lineTools}>
        <CopyButton value={m.text}>{({ copied, copy }) => (
          <Tooltip label={copied ? t('chat.copied') : t('chat.copy')}>
            <ActionIcon variant="subtle" color="gray" size="sm" onClick={copy} aria-label={t('chat.copy')}>{copied ? <IconCheck size={14} /> : <IconCopy size={14} />}</ActionIcon>
          </Tooltip>
        )}</CopyButton>
      </Group>
    </div>
  );
});
