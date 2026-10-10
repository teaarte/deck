import { useState } from 'react';
import { Button, Chip, Group, Stack, Text, TextInput } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Action, Data } from '../lib/catalog.gen';
import { ActionButton } from './actions/ActionButton';

type Question = NonNullable<NonNullable<Data['items']>[number]['questions']>[number];

// QuestionPicker answers the questions of an agent: options to pick, or a text of the owner's own.
export function QuestionPicker({ questions, act }: { questions: Question[]; act?: Action }) {
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
        {ready ? <ActionButton act={act} params={{ [act.input ?? 'answer']: JSON.stringify(all) }} />
          : <Button size="xs" disabled>{act.label || t('verb.' + (act.verb ?? 'answer'))}</Button>}
      </Group>
    </Stack>
  );
}
