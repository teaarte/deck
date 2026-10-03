import { useRef, useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { ActionIcon, Button, Chip, Code, Group, Menu, Modal, Select, Stack, Text, TextInput } from '@mantine/core';
import { useInterval } from '@mantine/hooks';
import { IconArchive, IconArrowBackUp, IconArrowUpRight, IconCheck, IconDownload, IconLogin, IconMessage, IconPlayerPause,
  IconDots, IconPlayerPlay, IconPlayerStop, IconRefresh, IconSend, IconSettings, IconTrash, IconX, type Icon } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { notifications } from '@mantine/notifications';
import { download } from './api';
import { NamedIcon } from './icons';
import i18n from './i18n';
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

type Before = () => Promise<void>;

// verbIcons are the icons of deck's own verbs; a button with a label and no icon of its own has none.
const verbIcons: Record<string, Icon> = {
  open: IconArrowUpRight, stop: IconPlayerStop, cancel_run: IconPlayerStop, run_now: IconPlayerPlay, resume: IconPlayerPlay,
  continue: IconPlayerPlay, pause: IconPlayerPause, retry: IconRefresh, remove: IconTrash, delete: IconTrash, archive: IconArchive,
  download: IconDownload, allow: IconCheck, yes: IconCheck, dismiss: IconCheck, mark_done: IconCheck, deny: IconX,
  answer: IconMessage, tell: IconMessage, send: IconSend, undo: IconArrowBackUp, settings: IconSettings, sign_in: IconLogin,
};
const verbIcon = (act: Action) => {
  if (act.icon) return <NamedIcon name={act.icon} fallback={act.label || act.verb || ''} size={14} stroke={2} />;
  const I = !act.label && act.verb ? verbIcons[act.verb] : undefined;
  return I ? <I size={14} stroke={2} /> : undefined;
};

function ActionButton({ act, context, params, onDone, before, inMenu }: { act: Action; context: ComponentContext; params?: Record<string, unknown>; onDone?: () => void; before?: Before; inMenu?: boolean }) {
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
        await before?.();
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
      await before?.();
      const all = { ...act.params, ...params, ...extra };
      const ok = await dispatch(context, { ...act, params: all });
      if (ok) {
        setTyping(false);
        setText('');
        if (act.undo) offerUndo(act.undo, label, context);
        onDone?.();
      }
    } catch (e) {
      notifications.show({ title: label, message: (e as Error).message, color: 'red', autoClose: 10000 });
    } finally {
      running.current = false;
      setBusy(false);
    }
  };
  if (act.input && !params) {
    if (!typing) {
      return <Button size="xs" variant={inMenu ? 'subtle' : act.primary ? 'filled' : 'default'} justify={inMenu ? 'flex-start' : undefined} fullWidth={inMenu} leftSection={verbIcon(act)} data-verb={act.verb} onClick={() => setTyping(true)}>{label}</Button>;
    }
    return (
      <Group gap="xs" wrap="nowrap">
        {act.options ? (
          <Select size="xs" autoFocus placeholder={t('input.' + act.input, { defaultValue: '' })} data={act.options} value={text || null}
            onChange={(v) => setText(v ?? '')} data-options />
        ) : (
          <TextInput size="xs" autoFocus placeholder={t('input.' + act.input, { defaultValue: '' })} value={text}
            onChange={(e) => setText(e.currentTarget.value)}
            onKeyDown={(e) => { if (e.key === 'Enter' && text.trim()) void send({ [act.input!]: text }); }} />
        )}
        <Button size="xs" loading={busy} disabled={!text.trim()} onClick={() => send({ [act.input!]: text })}>{t('verb.send')}</Button>
        <Button size="xs" variant="subtle" onClick={() => setTyping(false)}>{t('verb.cancel')}</Button>
      </Group>
    );
  }
  const button = (
    <Button size="xs" variant={inMenu ? 'subtle' : act.primary ? 'filled' : 'default'} justify={inMenu ? 'flex-start' : undefined} fullWidth={inMenu}
      color={inMenu && ['delete', 'remove', 'revoke'].includes(act.verb ?? '') ? 'red' : undefined} loading={busy} data-verb={act.verb} leftSection={verbIcon(act)}
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

// rowOpen is what a click on a row does: the move to a screen of its primary button.
export function rowOpen(actions: Action[] | undefined, context: ComponentContext) {
  const act = actions?.find((a) => a.primary && a.screen);
  if (!act) return {};
  return {
    'data-opens': true,
    style: { cursor: 'pointer' },
    onClick: (e: { target: EventTarget }) => {
      if ((e.target as HTMLElement).closest('button, a, input, textarea, [role="button"], [role="combobox"]')) return;
      if (window.getSelection()?.toString()) return;
      void dispatch(context, act);
    },
  };
}

// dispatch sends a button's call or move to the screen and says whether it went through.
async function dispatch(context: ComponentContext, act: Action): Promise<boolean> {
  const ticket = String(++tickets);
  await context.dispatchAction({ event: { name: act.screen ? 'open' : 'command', context: { ...act, ticket } } });
  const ok = outcomes.get(ticket) !== false;
  outcomes.delete(ticket);
  return ok;
}

let undos = 0;

// offerUndo shows for a moment the call that takes back the one that just went through.
function offerUndo(undo: Action, label: string, context: ComponentContext) {
  const id = 'undo-' + ++undos;
  notifications.show({
    id, autoClose: 8000, message: (
      <Group justify="space-between" gap="xs" wrap="nowrap" data-undo>
        <Text size="sm">{label}</Text>
        <Button size="compact-xs" variant="light" onClick={() => { notifications.hide(id); void dispatch(context, undo); }}>
          {i18n.t('verb.undo')}
        </Button>
      </Group>
    ),
  });
}

// Actions are the buttons of an item, the primary first; past max the rest wait in a menu. before runs ahead of
// each call, onDone after one went through.
export function Actions({ actions, context, onDone, before, max = 3 }: { actions?: Action[]; context: ComponentContext; onDone?: () => void; before?: Before; max?: number }) {
  const { t } = useTranslation();
  const now = useNow();
  const shown = (actions ?? []).filter((a) => !a.until || Date.parse(a.until) > now);
  if (!shown.length) return null;
  const sorted = [...shown.filter((a) => a.primary), ...shown.filter((a) => !a.primary)];
  const cut = sorted.length > max ? max - 1 : sorted.length;
  const rest = sorted.slice(cut);
  return (
    <Group gap={6} wrap="wrap" data-actions>
      {sorted.slice(0, cut).map((a, i) => <ActionButton key={i} act={a} context={context} onDone={onDone} before={before} />)}
      {rest.length > 0 && (
        <Menu position="bottom-end" keepMounted closeOnItemClick={false} withinPortal shadow="md" radius="md">
          <Menu.Target>
            <ActionIcon variant="default" size={30} radius="md" aria-label={t('actions.more')} data-more><IconDots size={16} /></ActionIcon>
          </Menu.Target>
          <Menu.Dropdown p={4} miw={180}>
            <Stack gap={2}>
              {rest.map((a, i) => <ActionButton key={i} act={a} context={context} onDone={onDone} before={before} inMenu />)}
            </Stack>
          </Menu.Dropdown>
        </Menu>
      )}
    </Group>
  );
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
