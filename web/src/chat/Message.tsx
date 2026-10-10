import { lazy, memo, Suspense } from 'react';
import { ActionIcon, Badge, Button, CopyButton, Group, Stack, Text, Tooltip } from '@mantine/core';
import { IconCheck, IconCopy, IconPaperclip, IconTrash } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { call } from '../lib/api';
import catalogSchema from '../lib/catalogSchema';
import { formatted } from '../lib/format';
import { notifyError } from '../lib/notify';
import type { ChatValue } from '../lib/catalog.gen';
import { ActionButton } from '../ui/actions/ActionButton';
import { DekiHead, useMascot } from '../ui/deki/Deki';
import { Markdown } from '../ui/Markdown';
import s from './chat.module.css';
import { Processes } from './Turn';

const Media = lazy(() => import('../blocks/media/Media').then((m) => ({ default: m.Media })));

type ChatMessage = ChatValue['messages'][number];
type WorkValue = NonNullable<ChatMessage['work']>;

// cost is a price in dollars, exact to its cents or, below a cent, to two digits.
const cost = (usd: number) => new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD',
  ...(usd > 0 && usd < 0.01 ? { maximumSignificantDigits: 2 } : { maximumFractionDigits: 2 }) }).format(usd);

// Work is what of a reply needs the owner: the processes it started that failed or still wait, each with its log.
function Work({ w }: { w: WorkValue }) {
  if (!w.processes.length) return null;
  return <div className={s.work} data-work><Processes list={w.processes} /></div>;
}

// Meta is how a reply went in one line, the model, cost, time and steps; it shows with the tools of the line.
function Meta({ w }: { w: WorkValue }) {
  const { t } = useTranslation();
  const parts = [w.model || w.provider, w.cost_usd !== undefined ? cost(w.cost_usd) : '',
    w.subscription_usd !== undefined ? t('chat.subscription', { cost: cost(w.subscription_usd) }) : '',
    t('chat.worked', { time: formatted(w.seconds, { format: 'duration' }) }), w.steps > 0 ? t('chat.steps', { count: w.steps }) : ''].filter(Boolean);
  return <Text size="xs" c="dimmed" data-work-meta>{parts.join(' · ')}</Text>;
}

const failed = catalogSchema.final_states.filter((x) => x !== 'done');

type Cut = NonNullable<NonNullable<ChatMessage['reply']>['cut']>;

// Broken is what a reply that broke off did and left, each change with its undo, and the providers it can go again on.
function Broken({ cut, provider }: { cut: Cut; provider?: string }) {
  const { t } = useTranslation();
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
      {!!m.files?.length && <div className={s.made} data-message-files><Suspense><Media items={m.files} /></Suspense></div>}
      {m.work && <Work w={m.work} />}
      <Group gap={4} className={s.lineTools}>
        <CopyButton value={m.text}>{({ copied, copy }) => (
          <Tooltip label={copied ? t('chat.copied') : t('chat.copy')}>
            <ActionIcon variant="subtle" color="gray" size="sm" onClick={copy} aria-label={t('chat.copy')}>{copied ? <IconCheck size={14} /> : <IconCopy size={14} />}</ActionIcon>
          </Tooltip>
        )}</CopyButton>
        {m.work && <Meta w={m.work} />}
      </Group>
    </div>
  );
});
