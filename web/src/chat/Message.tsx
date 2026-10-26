import { lazy, memo, Suspense } from 'react';
import { ActionIcon, Badge, CopyButton, Group, Stack, Text, Tooltip } from '@mantine/core';
import { IconCheck, IconCopy, IconPaperclip, IconTrash } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { formatted, toneColor } from '../lib/format';
import type { ChatData } from './chatData';
import { ActionButton } from '../ui/actions/ActionButton';
import { ActionIconButton } from '../ui/actions/ActionIconButton';
import { DekiHead, useMascot } from '../ui/deki/Deki';
import { Details } from '../ui/Details';
import { Markdown } from '../ui/Markdown';
import s from './chat.module.css';
import { Processes } from './Turn';

import { Thumb } from '../blocks/media/Thumb';
import { locale } from '../lib/i18n';

const Media = lazy(() => import('../blocks/media/Media').then((m) => ({ default: m.Media })));

type ChatMessage = ChatData['messages'][number];
type WorkValue = NonNullable<ChatMessage['work']>;

// cost is a price in dollars, exact to its cents or, below a cent, to two digits.
const cost = (usd: number) => new Intl.NumberFormat(locale(), { style: 'currency', currency: 'USD',
  ...(usd > 0 && usd < 0.01 ? { maximumSignificantDigits: 2 } : { maximumFractionDigits: 2 }) }).format(usd);

// Work is what of a reply needs the owner: the processes it started that failed or still wait, each with its log.
function Work({ w }: { w: WorkValue }) {
  if (!w.processes.length) return null;
  return <div className={s.work} data-work><Processes list={w.processes} /></div>;
}

// Meta is how a reply went in one line, the model, cost, time and steps; a phone has no hover, so it always shows.
function Meta({ w }: { w: WorkValue }) {
  const { t } = useTranslation();
  const parts = [w.model || w.provider, w.cost_usd !== undefined ? cost(w.cost_usd) : '',
    w.subscription_usd !== undefined ? t('chat.subscription', { cost: cost(w.subscription_usd) }) : '',
    t('chat.worked', { time: formatted(w.seconds, { format: 'duration' }) }), w.steps > 0 ? t('chat.steps', { count: w.steps }) : ''].filter(Boolean);
  return <Text size="xs" c="dimmed" data-work-meta>{parts.join(' · ')}</Text>;
}

type Told = NonNullable<ChatMessage['told']>;

// Told is a line deck wrote itself: how a work started from the topic ended, with its result, why it failed or what
// it waits for, and the button that opens its process.
function Told({ m, told }: { m: ChatMessage; told: Told }) {
  const { t } = useTranslation();
  const failed = told.tone === 'error';
  return (
    <Stack gap={6} className={s.told} data-message="deck" data-told={told.state} data-tone={told.tone}>
      <Group gap={6} wrap="nowrap" justify="space-between">
        <Text size="sm" fw={650} data-told-head>
          {t('chat.told', { work: `${told.project}.${told.command}` })} <Text span c={toneColor(told.tone) ?? 'dimmed'} inherit>{told.word}</Text>
        </Text>
        <ActionButton act={{ ...told.open, label: t('chat.told_open'), verb: undefined }} />
      </Group>
      {failed && told.reason && <Text size="sm">{told.reason}</Text>}
      {failed && told.error && <Details text={told.error} />}
      {told.state === 'done' && m.text && <Markdown text={m.text} />}
    </Stack>
  );
}

type Reply = NonNullable<ChatMessage['reply']>;
type Cut = NonNullable<Reply['cut']>;

// Broken is a reply that broke off or the owner stopped: why in words, how far it got, what it left with its undo,
// and the providers it can go again on, the first the main action of the latest one.
function Broken({ cut, reply, latest }: { cut: Cut; reply: Reply; latest: boolean }) {
  const { t } = useTranslation();
  const head = cut.stopped ? t('chat.stopped_by_you') : t('chat.cut_by', { provider: reply.provider || '?' }) + (cut.reason ? ': ' + cut.reason : '');
  return (
    <Stack gap={8} className={s.cut} data-reply-cut data-stopped={cut.stopped || undefined}>
      <Stack gap={2}>
        <Text size="sm" fw={650}>{head}</Text>
        <Text size="xs" c="dimmed" data-cut-steps>
          {cut.steps > 0 ? t('chat.cut_steps', { count: cut.steps }) : t('chat.cut_nothing')}{cut.said ? ' ' + t('chat.cut_said', { said: cut.said }) : ''}
        </Text>
        {!cut.stopped && !cut.reason && reply.error && <Text size="xs" c="dimmed" lineClamp={2} title={reply.error}>{reply.error}</Text>}
      </Stack>
      {(cut.did.length > 0 || (cut.reason && reply.error)) && (
        <Details text={[...(cut.reason && reply.error ? [reply.error, ''] : []), ...cut.did].join('\n')} />
      )}
      {cut.undo.length > 0 && (
        <Stack gap={4}>
          <Text size="xs" fw={600}>{t('chat.cut_left')}</Text>
          {cut.undo.map((u, i) => (
            <Group key={i} gap={6} wrap="nowrap" justify="space-between" className={s.left}>
              <Text size="xs" lineClamp={2}>{u.text}</Text>
              <ActionButton act={u.action} />
            </Group>
          ))}
        </Stack>
      )}
      {cut.retry.length > 0 && (
        <Group gap={6}>
          {cut.retry.map((r, i) => (
            <ActionButton key={r.label ?? r.verb} act={{ ...r, primary: latest && i === 0 && !cut.stopped, label: r.label ? t('chat.retry_on', { provider: r.label }) : undefined }} />
          ))}
        </Group>
      )}
    </Stack>
  );
}

// ReplyState tells that the reply to a message of the owner failed, and why, or what a broken one left.
function ReplyState({ reply, latest }: { reply: Reply; latest: boolean }) {
  if (reply.cut) return <Broken cut={reply.cut} reply={reply} latest={latest} />;
  return (
    <Group gap={4} justify="flex-end">
      <Badge size="xs" color={reply.tone === 'error' ? 'red' : 'gray'} variant="light" data-reply-state={reply.state}>{reply.word}</Badge>
      {reply.error && <Text size="xs" c="dimmed" lineClamp={2}>{reply.error}</Text>}
    </Group>
  );
}

// Message is a message of the owner as a bubble with its files, or a line of the master in markdown with its tools and
// the face of Deki; latest marks the last one of the topic.
export const Message = memo(function Message({ m, latest }: { m: ChatMessage; latest: boolean }) {
  const { t } = useTranslation();
  const mascot = useMascot();
  if (m.role === 'deck') return m.told ? <Told m={m} told={m.told} /> : null;
  if (m.role === 'owner') {
    // A picture attached shows as its light copy, another file by its name.
    const pictures = (m.files ?? []).filter((f) => f.thumb);
    const files = (m.files ?? []).filter((f) => !f.thumb).map((f) => f.name);
    return (
      <Stack gap={4} align="flex-end" data-message="owner">
        <div className={s.ownerBubble} data-bubble>
          {pictures.length > 0 && <Group gap={4} mb={6}>{pictures.map((f) => <Thumb key={f.id} it={f} size={72} />)}</Group>}
          {files.length > 0 && <Group gap={4} mb={6}>{files.map((f) => <Badge key={f} size="sm" variant="white" leftSection={<IconPaperclip size={11} />}>{f}</Badge>)}</Group>}
          <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>{m.text}</Text>
        </div>
        {(m.reply?.tone === 'error' || m.reply?.tone === 'muted') && <ReplyState reply={m.reply} latest={latest} />}
        {m.delete && (
          <Group gap={2} data-not-taken>
            <Text size="xs" c="dimmed">{t('chat.not_taken')}</Text>
            <ActionIconButton act={m.delete} icon={<IconTrash size={14} />} />
          </Group>
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
            <ActionIcon variant="subtle" color="gray" size="sm" className={s.copy} onClick={copy} aria-label={t('chat.copy')}>{copied ? <IconCheck size={14} /> : <IconCopy size={14} />}</ActionIcon>
          </Tooltip>
        )}</CopyButton>
        {m.work && <Meta w={m.work} />}
      </Group>
    </div>
  );
});
