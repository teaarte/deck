import { useRef, useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Paper, Stack } from '@mantine/core';
import type { ChatValue } from '../lib/catalog.gen';
import { QuestionPicker } from '../ui/QuestionPicker';
import { Composer } from './Composer';
import { Feed } from './Feed';
import { TopicBar } from './TopicBar';
import { Turn } from './Turn';
import { useAttachments } from './useAttachments';
import { useInputHistory } from './useInputHistory';
import { useChatSend } from './useChatSend';
import { useStickToBottom } from './useStickToBottom';
import { useTopics } from './useTopics';

// Chat is the chat with the master: topics, the feed of one, its questions, the turn going on and the input.
export function Chat({ value, context }: { value: ChatValue; context: ComponentContext }) {
  const [text, setText] = useState('');
  // model is the one picked for a new topic before its first message.
  const [model, setModel] = useState('');
  const topics = useTopics(value, context);
  const history = useInputHistory();
  const stick = useStickToBottom(value.messages[value.messages.length - 1]?.id, value.turn?.lines.length, value.topic?.id);
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
  return (
    <Stack gap="sm" h="calc(100dvh - 190px)" mih={360} data-chat>
      <TopicBar value={value} topics={topics} model={model} setModel={setModel} />
      <Feed messages={value.messages} stick={stick} />
      {value.questions.map((q) => (
        <Paper key={q.ask} withBorder p="sm" radius="lg" data-question={q.ask}>
          <QuestionPicker questions={q.questions} act={q.answer} />
        </Paper>
      ))}
      {value.turn && <Turn turn={value.turn} />}
      <Composer text={text} setText={setText} files={files} busy={chat.busy} onNewTopic={() => topics.pick('new')}
        onSend={(detached) => { if (text.trim()) history.push(text.trim()); void chat.send(text, detached); }} onWalk={history.walk}
        onEdit={(v) => { setText(v); chat.forgetKey(); }} />
    </Stack>
  );
}
