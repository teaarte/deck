import { useEffect, useRef, useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Badge, Box, Button, Group, Loader, Paper, ScrollArea, Select, Stack, Text, Textarea } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { useTranslation } from 'react-i18next';
import { call } from './api';
import { Actions, QuestionPicker } from './actions';
import { Markdown } from './document';
import type { ChatValue } from './catalog.gen';

export interface ChatScreen {
  project?: string;
  screen: string;
  record?: number;
}

// The screen the owner is on goes with each message, so the master knows what "this" is.
let current: ChatScreen = { screen: 'cockpit' };
export const setChatScreen = (s: ChatScreen) => { current = s; };

function screenOf(s: ChatScreen): string {
  const out: Record<string, unknown> = { screen: s.screen };
  if (s.project) out.project = s.project;
  if (s.record) out[s.screen === 'process' && !s.project ? 'process' : 'record'] = s.record;
  return JSON.stringify(out);
}

const failed = ['error', 'stopped', 'interrupted'];

// Chat is the chat with the master: topics, the feed of one, its questions, the turn going on and the input.
export function Chat({ value, context }: { value: ChatValue; context: ComponentContext }) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const feed = useRef<HTMLDivElement>(null);
  const pick = (topic: string) =>
    void context.dispatchAction({ event: { name: 'filter', context: { block: context.componentModel.id, field: 'topic', value: topic } } });
  const topic = value.topic;
  const last = value.messages[value.messages.length - 1]?.id;
  useEffect(() => { feed.current?.scrollTo({ top: feed.current.scrollHeight }); }, [last, value.turn?.lines.length]);

  const send = async () => {
    const message = text.trim();
    if (!message || busy) return;
    setBusy(true);
    try {
      const out = await call<{ topic: number }>(value.send.command!, {
        ...value.send.params, text: message, screen: screenOf(current), retry_key: crypto.randomUUID(),
      });
      setText('');
      if (!topic || out.topic !== topic.id) pick(String(out.topic));
    } catch (e) {
      notifications.show({ title: t('chat.send_failed'), message: (e as Error).message, color: 'red', autoClose: 10000 });
    } finally {
      setBusy(false);
    }
  };

  const options = [
    ...value.topics.map((x) => ({ value: String(x.id), label: x.title })),
    ...value.archived.map((x) => ({ value: String(x.id), label: t('chat.archived', { title: x.title }) })),
  ];
  return (
    <Stack gap="sm" data-chat>
      <Group gap="xs" wrap="nowrap">
        <Select size="xs" style={{ flex: 1 }} placeholder={t('chat.new_topic')} data={options} value={topic ? String(topic.id) : null}
          onChange={(v) => pick(v ?? 'new')} data-topics searchable nothingFoundMessage={t('chat.no_topics')} />
        <Button size="xs" variant="default" onClick={() => pick('new')} data-new-topic>{t('chat.new')}</Button>
      </Group>
      {topic && <Actions actions={[value.archive!, value.delete!].filter(Boolean)} context={context} />}
      <ScrollArea.Autosize mah="calc(100dvh - 330px)" viewportRef={feed} data-feed>
        <Stack gap="xs" pr="xs">
          {!value.messages.length && <Text size="sm" c="dimmed">{t('chat.empty')}</Text>}
          {value.messages.map((m) => (
            <Group key={m.id} justify={m.role === 'owner' ? 'flex-end' : 'flex-start'} data-message={m.role}>
              <Paper maw="88%" px="sm" py={6} radius="md" withBorder={m.role === 'master'}
                bg={m.role === 'owner' ? 'var(--mantine-primary-color-light)' : undefined}>
                {m.role === 'master' ? <Markdown text={m.text} /> : <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>{m.text}</Text>}
                {m.reply && failed.includes(m.reply.state) && (
                  <Group gap={4} mt={4}>
                    <Badge size="xs" color="red" variant="light" data-reply-state={m.reply.state}>{t('chat.reply_' + m.reply.state, { defaultValue: m.reply.state })}</Badge>
                    {m.reply.error && <Text size="xs" c="dimmed" lineClamp={2}>{m.reply.error}</Text>}
                    {m.reply.open && <Actions actions={[m.reply.open]} context={context} />}
                  </Group>
                )}
              </Paper>
            </Group>
          ))}
        </Stack>
      </ScrollArea.Autosize>
      {value.questions.map((q) => (
        <Paper key={q.ask} withBorder p="sm" radius="md" data-question={q.ask}>
          <QuestionPicker questions={q.questions} act={q.answer} context={context} />
        </Paper>
      ))}
      {value.turn && (
        <Paper withBorder p="xs" radius="md" data-turn={value.turn.state}>
          <Group justify="space-between" wrap="nowrap">
            <Group gap="xs" wrap="nowrap">
              {value.turn.state === 'running' && <Loader size="xs" />}
              <Text size="xs">{value.turn.waits ? t('chat.waits', { what: value.turn.waits }) : t('chat.replying', { provider: value.turn.provider ?? '' })}</Text>
            </Group>
            <Actions actions={[value.turn.stop!, value.turn.open!].filter(Boolean)} context={context} />
          </Group>
          <Box mt={4}>
            {value.turn.lines.filter((l) => l.kind === 'tool').slice(-6).map((l, i) => (
              <Text key={i} size="xs" ff="monospace" c="dimmed" truncate data-turn-line>{l.title}</Text>
            ))}
          </Box>
        </Paper>
      )}
      <Stack gap={4}>
        <Textarea autosize minRows={2} maxRows={8} placeholder={t('chat.placeholder')} value={text} data-chat-input
          onChange={(e) => setText(e.currentTarget.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); void send(); } }} />
        <Group justify="flex-end">
          <Button size="xs" loading={busy} disabled={!text.trim()} onClick={() => void send()} data-chat-send>{t('verb.send')}</Button>
        </Group>
      </Stack>
    </Stack>
  );
}
