import { useEffect, useRef, useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { ActionIcon, Anchor, Badge, Button, CloseButton, CopyButton, Group, Loader, Menu, Paper, ScrollArea, Select, Stack, Text, Textarea, Tooltip } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconArrowUp, IconCheck, IconCopy, IconMapPin, IconMessagePlus, IconMicrophone, IconPaperclip, IconPlayerStopFilled, IconPlus } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { call, upload } from './lib/api';
import { record, type Recording } from './voice';
import { useRoute } from './router';
import { Actions, ActionsMenu, QuestionPicker } from './actions';
import { Markdown } from './document';
import type { ChatValue } from './lib/catalog.gen';
import catalogSchema from './lib/catalogSchema';
import s from './shell.module.css';

export interface ChatScreen {
  project?: string;
  screen: string;
  record?: number;
}

// The screen the owner is on, and the card open on it, go with each message, so the master knows what "this" is.
let current: ChatScreen = { screen: 'cockpit' };
let card: number | undefined;
export const setChatScreen = (s: ChatScreen) => { current = s; card = undefined; };
export const setChatCard = (id?: number) => { card = id; };

function screenOf(s: ChatScreen): string {
  const out: Record<string, unknown> = { screen: s.screen };
  if (s.project) out.project = s.project;
  if (s.record) out[s.screen === 'process' && !s.project ? 'process' : 'record'] = s.record;
  if (card) out.card = card;
  return JSON.stringify(out);
}

// A topic another screen opened waits here until the chat shows it.
let wanted: number | undefined;
const opens = new Set<(topic: number) => void>();

// openTopic shows a topic in the chat panel and opens the panel.
export function openTopic(topic: number) {
  wanted = topic;
  opens.forEach((f) => f(topic));
  window.dispatchEvent(new Event('deck-chat-open'));
}

// newKey is a retry key of a draft; it works on plain http, where randomUUID does not.
function newKey(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, '0')).join('');
}

const failed = catalogSchema.final_states.filter((s) => s !== 'done');
const ALL = '*';
const NONE = '-';

// Chat is the chat with the master: topics, the feed of one, its questions, the turn going on and the input.
export function Chat({ value, context }: { value: ChatValue; context: ComponentContext }) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const feed = useRef<HTMLDivElement>(null);
  const key = useRef('');
  const pinned = useRef(false);
  const rec = useRef<Recording | undefined>(undefined);
  const [recording, setRecording] = useState(0);
  const [hearing, setHearing] = useState(false);
  const [files, setFiles] = useState<File[]>([]);
  const [dropping, setDropping] = useState(false);
  const [scope, setScope] = useState<string | null>(null);
  // detached sends the message without the screen the owner is on, so a new topic belongs to no module.
  const route = useRoute();
  const [detached, setDetached] = useState(false);
  useEffect(() => setDetached(false), [route.project, route.screen, route.record]);
  const picker = useRef<HTMLInputElement>(null);
  // sent are the ids of the files of the message being sent, so a retry does not upload them again.
  const sent = useRef<string[] | undefined>(undefined);
  useEffect(() => {
    if (!recording) return;
    const timer = setInterval(() => setRecording((n) => n + 1), 1000);
    return () => clearInterval(timer);
  }, [recording > 0]);
  useEffect(() => () => rec.current?.cancel(), []);
  const listen = async () => {
    try {
      rec.current = await record(() => void hear());
      setRecording(1);
    } catch (e) {
      notifications.show({ title: t('chat.mic_failed'), message: (e as Error).message, color: 'red' });
    }
  };
  const hear = async () => {
    const r = rec.current;
    rec.current = undefined;
    setRecording(0);
    if (!r) return;
    setHearing(true);
    try {
      const blob = await r.stop();
      const [f] = await upload('master', [new File([blob], 'voice.wav', { type: 'audio/wav' })]);
      const out = await call<{ text: string }>('deck.transcribe', { file: String(f.id) });
      setText((prev) => (prev.trim() ? prev.trimEnd() + ' ' : '') + out.text);
    } catch (e) {
      notifications.show({ title: t('chat.voice_failed'), message: (e as Error).message, color: 'red', autoClose: 10000 });
    } finally {
      setHearing(false);
    }
  };
  const pick = (topic: string) => {
    pinned.current = true;
    void context.dispatchAction({ event: { name: 'filter', context: { block: context.componentModel.id, field: 'topic', value: topic } } });
  };
  const topic = value.topic;
  useEffect(() => {
    const show = (id: number) => { wanted = undefined; pick(String(id)); };
    opens.add(show);
    if (wanted) show(wanted);
    return () => { opens.delete(show); };
  }, []);
  useEffect(() => { if (topic && !pinned.current) pick(String(topic.id)); }, [topic?.id]);
  // The feed sticks to its last reply while the owner is at the bottom, also when a reply grows after it is drawn.
  const stick = useRef(true);
  const top = useRef(0);
  // touched is when the owner last moved the feed; a scroll the browser makes when the feed shrinks is not theirs.
  const touched = useRef(0);
  const hand = () => { touched.current = Date.now(); };
  const content = useRef<HTMLDivElement>(null);
  const down = () => { if (feed.current && stick.current) feed.current.scrollTop = feed.current.scrollHeight; };
  useEffect(() => {
    const ro = new ResizeObserver(down);
    if (content.current) ro.observe(content.current);
    if (feed.current) ro.observe(feed.current);
    return () => ro.disconnect();
  }, []);
  const last = value.messages[value.messages.length - 1]?.id;
  useEffect(down, [last, value.turn?.lines.length]);
  useEffect(() => { stick.current = true; down(); }, [topic?.id]);

  const send = async () => {
    const message = text.trim();
    if (!message || busy) return;
    setBusy(true);
    stick.current = true;
    down();
    key.current ||= newKey();
    try {
      if (files.length && !sent.current) sent.current = (await upload('master', files)).map((f) => String(f.id));
      const out = await call<{ topic: number }>(value.send.command!, {
        ...value.send.params, ...(!(value.send.params as { topic?: number } | undefined)?.topic && card ? { card } : {}),
        text: message, ...(detached ? {} : { screen: screenOf(current) }), retry_key: key.current, ...(sent.current ? { files: sent.current } : {}),
      });
      setText('');
      setFiles([]);
      sent.current = undefined;
      key.current = '';
      stick.current = true;
      pick(String(out.topic));
    } catch (e) {
      notifications.show({ title: t('chat.send_failed'), message: (e as Error).message, color: 'red', autoClose: 10000 });
    } finally {
      setBusy(false);
    }
  };

  const projects = [...new Set([...value.topics, ...value.archived].map((x) => (x as { project?: string }).project).filter(Boolean) as string[])].sort();
  const inProject = (x: { project?: string }) => !scope || (scope === NONE ? !x.project : x.project === scope);
  const options = [
    ...value.topics.filter(inProject).map((x) => ({ value: String(x.id), label: x.title })),
    ...value.archived.filter(inProject).map((x) => ({ value: String(x.id), label: t('chat.archived', { title: x.title }) })),
  ];
  const attach = (list: FileList | File[] | null) => { if (list?.length) { sent.current = undefined; setFiles((f) => [...f, ...Array.from(list)]); } };
  return (
    <Stack gap="sm" h="calc(100dvh - 190px)" mih={360} data-chat>
      <Group gap={6} wrap="nowrap">
        <Select size="xs" radius="xl" style={{ flex: 1 }} placeholder={t('chat.new_topic')} data={options} value={topic ? String(topic.id) : null}
          onChange={(v) => pick(v ?? 'new')} data-topics searchable nothingFoundMessage={t('chat.no_topics')} />
        {projects.length > 0 && (
          <Select size="xs" radius="xl" w={140} allowDeselect={false} data-topic-project aria-label={t('chat.module')}
            data={[{ value: ALL, label: t('chat.all_modules') }, { value: NONE, label: t('chat.no_module') }, ...projects.map((p) => ({ value: p, label: p }))]}
            value={scope ?? ALL} onChange={(v) => setScope(v === ALL ? null : v)} />
        )}
        <Tooltip label={t('chat.new_topic')}>
          <ActionIcon variant="default" radius="xl" size={30} onClick={() => pick('new')} aria-label={t('chat.new_topic')} data-new-topic><IconPlus size={16} /></ActionIcon>
        </Tooltip>
        {topic && <ActionsMenu actions={[value.archive!, value.delete!].filter(Boolean)} context={context} />}
      </Group>
      <ScrollArea style={{ flex: 1, minHeight: 80 }} viewportRef={feed} data-feed
        onWheel={hand} onTouchMove={hand} onKeyDown={hand} onPointerDown={hand}
        onScrollPositionChange={({ y }) => {
          const v = feed.current;
          if (!v) return;
          if (y < top.current && Date.now() - touched.current < 800) stick.current = false;
          if (v.scrollHeight - y - v.clientHeight < 48) stick.current = true;
          top.current = y;
        }}>
        <Stack gap="md" pr="xs" py="xs" ref={content}>
          {!value.messages.length && <Text size="sm" c="dimmed" ta="center" mt="xl">{t('chat.empty')}</Text>}
          {value.messages.map((m) => {
            const files = (m.files ?? []).map((f) => f.name);
            return m.role === 'owner' ? (
              <Stack key={m.id} gap={4} align="flex-end" data-message="owner">
                <div className={s.ownerBubble}>
                  {files.length > 0 && <Group gap={4} mb={6}>{files.map((f) => <Badge key={f} size="sm" variant="white" radius="sm" leftSection={<IconPaperclip size={11} />}>{f}</Badge>)}</Group>}
                  <Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>{m.text}</Text>
                </div>
                {m.reply && failed.includes(m.reply.state) && (
                  <Group gap={4} justify="flex-end">
                    <Badge size="xs" color="red" variant="light" data-reply-state={m.reply.state}>{t('chat.reply_' + m.reply.state, { defaultValue: m.reply.state })}</Badge>
                    {m.reply.error && <Text size="xs" c="dimmed" lineClamp={2}>{m.reply.error}</Text>}
                  </Group>
                )}
              </Stack>
            ) : (
              <div key={m.id} className={s.masterLine} data-message="master">
                <Markdown text={m.text} />
                {m.reply && failed.includes(m.reply.state) && (
                  <Group gap={4} mt={4}>
                    <Badge size="xs" color="red" variant="light" data-reply-state={m.reply.state}>{t('chat.reply_' + m.reply.state, { defaultValue: m.reply.state })}</Badge>
                    {m.reply.error && <Text size="xs" c="dimmed" lineClamp={2}>{m.reply.error}</Text>}
                  </Group>
                )}
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
          })}
        </Stack>
      </ScrollArea>
      {value.questions.map((q) => (
        <Paper key={q.ask} withBorder p="sm" radius="lg" data-question={q.ask}>
          <QuestionPicker questions={q.questions} act={q.answer} context={context} />
        </Paper>
      ))}
      {value.turn && (
        <div className={s.turn} data-turn={value.turn.state}>
          <Group justify="space-between" wrap="nowrap">
            <Group gap="xs" wrap="nowrap" miw={0}>
              {value.turn.state === 'running' && <Loader size="xs" type="dots" />}
              <Text size="xs" c="dimmed" truncate>{value.turn.waits ? t('chat.waits', { what: value.turn.waits }) : t('chat.replying', { provider: value.turn.provider ?? '' })}</Text>
            </Group>
            <Actions actions={[value.turn.stop!, value.turn.open!].filter(Boolean)} context={context} />
          </Group>
          {value.turn.lines.filter((l) => l.kind === 'tool').slice(-4).map((l, i) => (
            <Text key={i} size="xs" ff="monospace" c="dimmed" truncate data-turn-line>{l.title}</Text>
          ))}
        </div>
      )}
      <div className={s.composer} data-composer data-drop={dropping || undefined}
        onDragOver={(e) => { e.preventDefault(); setDropping(true); }} onDragLeave={() => setDropping(false)}
        onDrop={(e) => { e.preventDefault(); setDropping(false); attach(e.dataTransfer.files); }}>
        {(route.project || route.screen) && (
          <Group gap={6} px={6} pt={2}>
            {detached ? (
              <Badge size="sm" radius="sm" variant="outline" color="gray" tt="none" style={{ cursor: "pointer" }} onClick={() => setDetached(false)} data-context-off>
                {t('chat.no_context')}
              </Badge>
            ) : (
              <Badge size="sm" radius="sm" variant="light" tt="none" leftSection={<IconMapPin size={11} />} data-context
                rightSection={<CloseButton size="xs" onClick={() => setDetached(true)} aria-label={t('chat.drop_context')} />}>
                {[route.project, route.screen].filter(Boolean).join(' · ')}
              </Badge>
            )}
          </Group>
        )}
        {files.length > 0 && (
          <Group gap={6} px={6} pt={4} data-attached>
            {files.map((f, i) => (
              <Badge key={i} size="lg" radius="md" variant="light" leftSection={<IconPaperclip size={12} />}
                rightSection={<CloseButton size="xs" onClick={() => setFiles(files.filter((_, j) => j !== i))} aria-label={t('chat.unattach')} />}>{f.name}</Badge>
            ))}
          </Group>
        )}
        <Textarea variant="unstyled" autosize minRows={1} maxRows={10} placeholder={t('chat.placeholder')} value={text} data-chat-input px={8}
          onChange={(e) => { setText(e.currentTarget.value); if (!e.currentTarget.value.trim()) key.current = ''; }}
          onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); void send(); } }} />
        <Group justify="space-between" gap={6}>
          <Menu position="top-start" shadow="md" radius="md">
            <Menu.Target>
              <ActionIcon variant="subtle" color="gray" radius="xl" size="lg" aria-label={t('chat.add')} data-composer-add><IconPlus size={20} /></ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item leftSection={<IconPaperclip size={16} />} onClick={() => picker.current?.click()} data-attach>{t('chat.attach')}</Menu.Item>
              <Menu.Item leftSection={<IconMessagePlus size={16} />} onClick={() => pick('new')}>{t('chat.new_topic')}</Menu.Item>
            </Menu.Dropdown>
          </Menu>
          <input ref={picker} type="file" multiple hidden onChange={(e) => { attach(e.currentTarget.files); e.currentTarget.value = ''; }} data-attach-input />
          <Group gap={6}>
            {recording ? (
              <Button size="xs" color="red" radius="xl" leftSection={<IconPlayerStopFilled size={12} />} onClick={() => void hear()} data-mic-stop>{t('chat.stop_recording', { s: recording - 1 })}</Button>
            ) : (
              <Tooltip label={t('chat.mic')}>
                <ActionIcon variant="subtle" color="gray" radius="xl" size="lg" loading={hearing} onClick={() => void listen()} aria-label={t('chat.mic')} data-mic><IconMicrophone size={19} /></ActionIcon>
              </Tooltip>
            )}
            <ActionIcon radius="xl" size="lg" variant="filled" loading={busy} disabled={!text.trim()} onClick={() => void send()} aria-label={t('verb.send')} data-chat-send>
              <IconArrowUp size={18} stroke={2.4} />
            </ActionIcon>
          </Group>
        </Group>
      </div>
    </Stack>
  );
}
