import { lazy, Suspense, useState } from 'react';
import { Button, Group, Stack, Text, UnstyledButton } from '@mantine/core';
import { IconCheck, IconChevronDown, IconChevronRight } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Action, ChartValue, Data } from '../lib/catalog.gen';
import type { Shown as Look } from '../lib/format';
import { locale } from '../lib/i18n';
import type { ChatData } from './chatData';
import { Card } from '../blocks/card';
import { Form, type FormValue } from '../blocks/form/Form';
import { DataTable } from '../blocks/table/DataTable';
import { ActionButton } from '../ui/actions/ActionButton';
import s from './chat.module.css';

const Chart = lazy(() => import('../blocks/chart/Chart').then((m) => ({ default: m.Chart })));

type Block = NonNullable<ChatData['messages'][number]['block']>;
type Option = { label: string; description?: string; action?: Action };

// at is when the owner answered, as the time alone today.
const at = (iso: string) => {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  const today = d.toDateString() === new Date().toDateString();
  return new Intl.DateTimeFormat(locale(), today ? { timeStyle: 'short' } : { dateStyle: 'medium', timeStyle: 'short' }).format(d);
};

// Choice is options as buttons that answer the block; once answered, the one picked stands out and none presses.
function Choice({ data, picked }: { data: Data; picked?: string }) {
  const options = ((data.value as { options?: Option[] } | undefined)?.options ?? []);
  return (
    <Group gap={6} data-shown-options>
      {options.map((o) => o.action
        ? <span key={o.label} title={o.description}><ActionButton act={o.action} /></span>
        : <Button key={o.label} size="xs" variant={o.label === picked ? 'filled' : 'default'} disabled={o.label !== picked} title={o.description}
          leftSection={o.label === picked ? <IconCheck size={14} /> : undefined} data-picked={o.label === picked || undefined}>{o.label}</Button>)}
    </Group>
  );
}

// Body is the block of the catalog a message shows, drawn by the block's own parts.
function Body({ b, still }: { b: Block; still: boolean }) {
  const props = (b.props ?? {}) as Look & { kind?: string; columns?: (Look & { label: string })[] };
  switch (b.type) {
    case 'choice':
      return <Choice data={b.data} picked={b.answered?.choice} />;
    case 'form':
      return <Form value={b.data.value as FormValue} still={still} />;
    case 'table':
      return <DataTable columns={props.columns ?? []} rows={b.data.rows ?? []} />;
    case 'chart':
      return <Suspense><Chart value={b.data.value as ChartValue} kind={props.kind} s={props} h={220} /></Suspense>;
    case 'card':
      return <Card d={b.data} />;
  }
  return null;
}

// Shown is the one block a message of the master shows under its text; a form answered folds into a line that opens
// it as it was sent, the answer itself being the owner's next message.
export function Shown({ b }: { b: Block }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const folded = b.type === 'form' && !!b.answered;
  if (folded) {
    return (
      <div className={s.shown} data-shown={b.type} data-answered>
        <UnstyledButton className={s.shownFold} onClick={() => setOpen(!open)} aria-expanded={open} data-shown-fold>
          <IconCheck size={15} className={s.shownDone} />
          <Text size="sm" fw={600} truncate className="deck-grow">{[b.title || t('Answered'), at(b.answered!.at)].filter(Boolean).join(' · ')}</Text>
          {open ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />}
        </UnstyledButton>
        {open && <div className={s.shownBody}><Body b={b} still /></div>}
      </div>
    );
  }
  return (
    <Stack gap={8} className={s.shown} data-shown={b.type} data-answered={b.answered ? true : undefined}>
      {b.title && <Text size="sm" fw={650}>{b.title}</Text>}
      <Body b={b} still={!!b.answered} />
    </Stack>
  );
}
