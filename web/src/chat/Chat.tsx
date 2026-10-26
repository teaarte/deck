import { useEffect, useRef, useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Group, Stack, Text, UnstyledButton } from '@mantine/core';
import { IconArrowUpRight, IconHandStop, IconPlayerPlay, IconSparkles } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useRoute } from '../app/router';
import type { ChatData } from './chatData';
import { readBlock } from '../screen/press';
import { ActionButton } from '../ui/actions/ActionButton';
import { Holding } from '../ui/actions/undo';
import { Deadline } from '../ui/Deadline';
import { Deki, useMascot, useMasterName } from '../ui/deki/Deki';
import { Details } from '../ui/Details';
import { NamedIcon } from '../ui/NamedIcon';
import { QuestionPicker } from '../ui/QuestionPicker';
import s from './chat.module.css';
import { setTurnLive } from './chatStore';
import { Composer } from './Composer';
import { Feed } from './Feed';
import { TopicBar } from './TopicBar';
import { Turn } from './Turn';
import { useAttachments } from './useAttachments';
import { useInputHistory } from './useInputHistory';
import { useChatSend } from './useChatSend';
import { useStickToBottom } from './useStickToBottom';
import { useTopics } from './useTopics';
import { useVoiceState } from './voiceStore';

type Decision = ChatData['asks'][number];

const ICONS: Record<string, typeof IconSparkles> = { sparkles: IconSparkles, 'player-play': IconPlayerPlay };

// Empty is a topic without messages: Deki at rest, what to do, and what the module of the screen can be asked.
function Empty({ suggest, put }: { suggest: ChatData['suggest']; put: (text: string) => void }) {
  const { t } = useTranslation();
  const mascot = useMascot();
  const name = useMasterName();
  const voice = useVoiceState();
  return (
    <Stack align="center" gap="sm" className={s.empty} data-chat-empty>
      {mascot && <Deki pose={voice.seconds > 0 ? 'listen' : 'chill'} size={190} still={voice.seconds === 0} />}
      <Text fz={19} fw={700} ta="center">{t('chat.what')}</Text>
      <Text size="sm" c="dimmed" ta="center">{t('chat.what_text', { name })}</Text>
      {suggest.length > 0 && (
        <Stack gap={8} w="100%" mt={4}>
          {suggest.map((x) => {
            const Icon = ICONS[x.icon];
            return (
              <UnstyledButton key={x.text} className={s.suggest} onClick={() => put(x.text)} data-suggest>
                <span className={s.suggestIcon}>{Icon ? <Icon size={17} /> : <NamedIcon name={x.icon} fallback={x.text} size={17} />}</span>
                <span style={{ flex: 1 }}>{x.text}</span>
                <IconArrowUpRight size={14} opacity={0.5} />
              </UnstyledButton>
            );
          })}
        </Stack>
      )}
    </Stack>
  );
}

// DecisionCard is what the turn waits for the owner to decide, in the topic: what, why and who asks, its details and
// its buttons; it steps aside while an answer that holds may still be taken back.
function DecisionCard({ d }: { d: Decision }) {
  const { t } = useTranslation();
  const [held, setHeld] = useState(false);
  if (held) return null;
  const details = [d.by ?? '', d.command ?? '', d.payload !== undefined && d.payload !== null ? JSON.stringify(d.payload, null, 2) : ''].filter(Boolean).join('\n');
  return (
    <Stack gap={8} className={s.decision} data-chat-ask={d.id} data-kind={d.kind}>
      <Group justify="space-between" gap="xs" wrap="nowrap">
        <Group gap={6} className={s.decisionHead} wrap="nowrap">
          <IconHandStop size={15} />
          <Text size="xs" fw={700}>{d.expired ? t('chat.ask_expired') : t('chat.ask_yes')}</Text>
        </Group>
        {d.expires_at && !d.expired && <Deadline until={d.expires_at} />}
      </Group>
      <Stack gap={2}>
        <Text size="sm" fw={650}>{d.what}</Text>
        {(d.why || d.where) && <Text size="sm" style={{ overflowWrap: 'anywhere' }}>{d.why || d.where}</Text>}
        <Text size="xs" c="dimmed">{[d.who && t('chat.ask_who', { who: d.who }), d.project].filter(Boolean).join(' · ')}</Text>
      </Stack>
      {details && <Details text={details} />}
      <Holding.Provider value={setHeld}>
        <Group gap={6}>{d.actions.map((a, i) => <ActionButton key={i} act={a} />)}</Group>
      </Holding.Provider>
    </Stack>
  );
}

// Chat is the chat with the master: topics, the feed of one with what its turn asks and does, and the input.
export function Chat({ value, context }: { value: ChatData; context: ComponentContext }) {
  const [text, setText] = useState('');
  // model is the one picked for a new topic before its first message.
  const [model, setModel] = useState('');
  const topics = useTopics(value, context);
  const history = useInputHistory();
  const route = useRoute();
  const stick = useStickToBottom(value.messages[value.messages.length - 1]?.id, (value.turn?.lines.length ?? 0) + value.asks.length + value.questions.length, value.topic?.id);
  const forget = useRef(() => undefined as void);
  const files = useAttachments(() => forget.current());
  const chat = useChatSend(value, {
    before: stick.follow,
    files: files.ids,
    model: () => (value.topic ? '' : model),
    sent: (topic) => {
      setText('');
      setModel('');
      files.clear();
      stick.keep();
      topics.pick(String(topic));
    },
  });
  forget.current = chat.forgetKey;
  const empty = value.messages.length === 0;
  // An empty topic suggests what the module of the screen the owner is on can be asked.
  const project = useRef<string>('');
  useEffect(() => {
    const p = route.project ?? '';
    if (!empty || p === project.current) return;
    project.current = p;
    readBlock(context, 'filter', { field: 'project', value: p });
  }, [empty, route.project]);
  const live = value.turn?.state === 'running';
  useEffect(() => { setTurnLive(live); return () => setTurnLive(false); }, [live]);
  return (
    <Stack gap="sm" h="calc(100dvh - 190px)" mih={360} data-chat>
      <TopicBar value={value} topics={topics} model={model} setModel={setModel} />
      <Feed messages={value.messages} stick={stick} empty={<Empty suggest={value.suggest} put={setText} />}>
        {value.questions.map((q) => (
          <div key={q.ask} className={s.question} data-question={q.ask}>
            <QuestionPicker questions={q.questions} act={q.answer} />
          </div>
        ))}
        {value.asks.map((d) => <DecisionCard key={d.id} d={d} />)}
      </Feed>
      {value.turn && <Turn turn={value.turn} />}
      <Composer text={text} setText={setText} files={files} busy={chat.busy} onNewTopic={() => topics.pick('new')}
        onSend={(detached) => { if (text.trim()) history.push(text.trim()); void chat.send(text, detached); }} onWalk={history.walk}
        onEdit={(v) => { setText(v); chat.forgetKey(); }} />
    </Stack>
  );
}
