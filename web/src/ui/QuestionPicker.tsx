import { useState } from 'react';
import { Button, Checkbox, Group, Radio, Stack, Text, TextInput } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Action, Question } from '../lib/catalog.gen';
import { ActionButton } from './actions/ActionButton';
import s from './QuestionPicker.module.css';

type Option = NonNullable<Question['options']>[number];

// Choice is an option as a card: its label and what it means, with a radio or a checkbox.
function Choice({ o, several }: { o: Option; several?: boolean }) {
  const Card = several ? Checkbox.Card : Radio.Card;
  return (
    <Card value={o.label ?? ''} className={s.option} data-option={o.label}>
      <Group wrap="nowrap" align="flex-start" gap="sm">
        {several ? <Checkbox.Indicator size="sm" /> : <Radio.Indicator size="sm" />}
        <div className={s.words}>
          <Text size="sm" fw={500}>{o.label}</Text>
          {o.description && <Text size="xs" c="dimmed" data-option-description>{o.description}</Text>}
        </div>
      </Group>
    </Card>
  );
}

// QuestionPicker answers the questions of an agent one at a time: options as cards with what each means, one or
// several, or a text of the owner's own; the last one sends every answer.
export function QuestionPicker({ questions, act, quiet }: { questions: Question[]; act?: Action; quiet?: boolean }) {
  const { t } = useTranslation();
  const [at, setAt] = useState(0);
  const [picked, setPicked] = useState<Record<string, string[]>>({});
  const [other, setOther] = useState<Record<string, string>>({});
  if (!act || questions.length === 0) return null;
  const answer = (q: Question) => {
    const o = other[q.question]?.trim();
    const p = picked[q.question] ?? [];
    return q.multiSelect ? [...p, ...(o ? [o] : [])].join(', ') : o || p[0] || '';
  };
  const i = Math.min(at, questions.length - 1);
  const q = questions[i];
  const last = i === questions.length - 1;
  const pick = (v: string[]) => setPicked({ ...picked, [q.question]: v });
  const head = [questions.length > 1 ? t('question.of', { n: i + 1, total: questions.length }) : '', q.header ?? '',
    q.multiSelect ? t('question.several') : ''].filter(Boolean).join(' · ');
  const all = Object.fromEntries(questions.map((x) => [x.question, answer(x)]));
  const label = act.label || t('verb.' + (act.verb ?? 'answer'));
  return (
    <Stack gap="xs" data-question-at={i + 1}>
      {head && <Text size="xs" c="dimmed" fw={600} data-question-head>{head}</Text>}
      <div className={s.body} data-question-body>
        <Text size="sm" fw={500} className={s.text} data-question-text>{q.question}</Text>
        {q.multiSelect ? (
          <Checkbox.Group value={picked[q.question] ?? []} onChange={pick}>
            <Stack gap={6}>{(q.options ?? []).map((o) => <Choice key={o.label} o={o} several />)}</Stack>
          </Checkbox.Group>
        ) : (
          <Radio.Group value={picked[q.question]?.[0] ?? null} onChange={(v) => pick([v])}>
            <Stack gap={6}>{(q.options ?? []).map((o) => <Choice key={o.label} o={o} />)}</Stack>
          </Radio.Group>
        )}
      </div>
      <TextInput size="xs" placeholder={t('question.other')} value={other[q.question] ?? ''}
        onChange={(e) => setOther({ ...other, [q.question]: e.currentTarget.value })} data-question-other />
      <Group justify="space-between" gap="xs">
        {i > 0 ? <Button size="xs" variant="subtle" onClick={() => setAt(i - 1)} data-question-back>{t('question.back')}</Button> : <span />}
        {!last ? <Button size="xs" variant={quiet ? 'light' : undefined} disabled={answer(q) === ''} onClick={() => setAt(i + 1)} data-question-next>{t('question.next')}</Button>
          : questions.every((x) => answer(x) !== '') ? <ActionButton act={act} params={{ [act.input ?? 'answer']: JSON.stringify(all) }} quiet={quiet} />
          : <Button size="xs" variant={quiet ? 'light' : undefined} disabled>{label}</Button>}
      </Group>
    </Stack>
  );
}
