import { useRef, useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Button, Chip, Code, Group, Modal, Stack, Text, TextInput } from '@mantine/core';
import { useInterval } from '@mantine/hooks';
import { useTranslation } from 'react-i18next';
import { notifications } from '@mantine/notifications';
import { download } from './api';
import type { Action, Data } from './catalog.gen';

type Question = NonNullable<NonNullable<Data['items']>[number]['questions']>[number];

// outcomes holds whether the call behind each pressed button went through, by the ticket the button sent.
export const outcomes = new Map<string, boolean>();
let tickets = 0;

const useNow = () => {
  const [now, setNow] = useState(() => Date.now());
  useInterval(() => setNow(Date.now()), 15000, { autoInvoke: true });
  return now;
};

function ActionButton({ act, context, params }: { act: Action; context: ComponentContext; params?: Record<string, unknown> }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const [typing, setTyping] = useState(false);
  const [text, setText] = useState('');
  const [asking, setAsking] = useState(false);
  const label = act.label || (act.verb ? t('verb.' + act.verb) : '');
  const send = async (extra?: Record<string, unknown>) => {
    if (running.current) return;
    if (act.file) {
      running.current = true;
      setBusy(true);
      try {
        await download(act.file);
      } catch (e) {
        notifications.show({ title: label, message: (e as Error).message, color: 'red', autoClose: 10000 });
      } finally {
        running.current = false;
        setBusy(false);
      }
      return;
    }
    running.current = true;
    setBusy(true);
    try {
      const all = { ...act.params, ...params, ...extra };
      const ticket = String(++tickets);
      await context.dispatchAction({ event: { name: act.screen ? 'open' : 'command', context: { ...act, params: all, ticket } } });
      const ok = outcomes.get(ticket) !== false;
      outcomes.delete(ticket);
      if (ok) {
        setTyping(false);
        setText('');
      }
    } finally {
      running.current = false;
      setBusy(false);
    }
  };
  if (act.input && !params) {
    if (!typing) {
      return <Button size="xs" variant={act.primary ? 'filled' : 'default'} data-verb={act.verb} onClick={() => setTyping(true)}>{label}</Button>;
    }
    return (
      <Group gap="xs" wrap="nowrap">
        <TextInput size="xs" autoFocus placeholder={t('input.' + act.input, { defaultValue: '' })} value={text}
          onChange={(e) => setText(e.currentTarget.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' && text.trim()) void send({ [act.input!]: text }); }} />
        <Button size="xs" loading={busy} disabled={!text.trim()} onClick={() => send({ [act.input!]: text })}>{t('verb.send')}</Button>
        <Button size="xs" variant="subtle" onClick={() => setTyping(false)}>{t('verb.cancel')}</Button>
      </Group>
    );
  }
  const button = (
    <Button size="xs" variant={act.primary ? 'filled' : 'default'} loading={busy} data-verb={act.verb}
      onClick={() => (act.confirm ? setAsking(true) : void send())}>{label}</Button>
  );
  if (!act.confirm) return button;
  return (
    <>
      {button}
      <Modal opened={asking} onClose={() => setAsking(false)} title={t('confirm.title', { action: label })}>
        <Stack gap="sm" data-confirm>
          <Code block>{JSON.stringify(act.params ?? {}, null, 2)}</Code>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setAsking(false)}>{t('verb.cancel')}</Button>
            <Button color="red" onClick={() => { setAsking(false); void send(); }} data-confirm-yes>{label}</Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}

export function Actions({ actions, context }: { actions?: Action[]; context: ComponentContext }) {
  const now = useNow();
  const shown = (actions ?? []).filter((a) => !a.until || Date.parse(a.until) > now);
  if (!shown.length) return null;
  return <Group gap="xs">{shown.map((a, i) => <ActionButton key={i} act={a} context={context} />)}</Group>;
}

export function QuestionPicker({ questions, act, context }: { questions: Question[]; act?: Action; context: ComponentContext }) {
  const { t } = useTranslation();
  const [picked, setPicked] = useState<Record<string, string | string[]>>({});
  const [other, setOther] = useState<Record<string, string>>({});
  const answer = (q: Question) => {
    const o = other[q.question]?.trim();
    const p = picked[q.question];
    if (q.multiSelect) return [...((p as string[]) ?? []), ...(o ? [o] : [])].join(', ');
    return o || (p as string) || '';
  };
  const ready = questions.every((q) => answer(q) !== '');
  if (!act) return null;
  const all = Object.fromEntries(questions.map((q) => [q.question, answer(q)]));
  return (
    <Stack gap="xs">
      {questions.map((q) => (
        <Stack key={q.question} gap={4}>
          <Text size="sm" fw={500}>{q.question}</Text>
          {q.multiSelect ? (
            <Chip.Group multiple value={(picked[q.question] as string[]) ?? []} onChange={(v) => setPicked({ ...picked, [q.question]: v })}>
              <Group gap="xs">{(q.options ?? []).map((o) => <Chip key={o.label} value={o.label} size="xs">{o.label}</Chip>)}</Group>
            </Chip.Group>
          ) : (
            <Chip.Group multiple={false} value={(picked[q.question] as string) ?? ''} onChange={(v) => setPicked({ ...picked, [q.question]: v })}>
              <Group gap="xs">{(q.options ?? []).map((o) => <Chip key={o.label} value={o.label} size="xs">{o.label}</Chip>)}</Group>
            </Chip.Group>
          )}
          <TextInput size="xs" placeholder={t('question.other')} value={other[q.question] ?? ''}
            onChange={(e) => setOther({ ...other, [q.question]: e.currentTarget.value })} />
        </Stack>
      ))}
      <Group>
        {ready ? <ActionButton act={act} context={context} params={{ [act.input ?? 'answer']: JSON.stringify(all) }} />
          : <Button size="xs" disabled>{act.label || t('verb.' + (act.verb ?? 'answer'))}</Button>}
      </Group>
    </Stack>
  );
}
