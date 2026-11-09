import { useState } from 'react';
import { Button, Group, Select, TextInput } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Action } from '../../lib/catalog.gen';
import { Mark, OptionLine, grouped, type Facts } from '../OptionLine';

// choices are the options of a button for a select: each value with its label in the owner's language.
export const choices = (act: Action) => (act.options ?? []).map((value, i) => ({ value, label: act.labels?.[i] ?? value }));

// choiceData are the choices under the heading of their group, when the button gives the groups.
export const choiceData = (act: Action) => grouped(choices(act), (o) => act.groups?.[act.options!.indexOf(o.value)]);

// factsOf are the mark, note and group of each option of a button by its value, when the button gives them.
export function factsOf(act: Action): Map<string, Facts> {
  return new Map((act.options ?? []).map((o, i) => [o, { logo: act.logos?.[i] || undefined, note: act.notes?.[i] || undefined, via: act.groups?.[i] || undefined }]));
}

// HINTS are the words of the field a button asks for, by its param.
const HINTS: Record<string, string> = { answer: 'Your answer', reason: 'Why not', remark: 'What to change', text: 'Your message' };

// ActionInput takes the one value a button asks for, typed or picked from its options.
export function ActionInput({ act, busy, onSend, onCancel }: { act: Action; busy: boolean; onSend: (text: string) => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const facts = factsOf(act);
  return (
    <Group gap="xs" wrap="nowrap">
      {act.options ? (
        <Select size="xs" autoFocus placeholder={act.input && HINTS[act.input] ? t(HINTS[act.input]) : ''} data={choiceData(act)} value={text || null}
          leftSection={text ? <Mark facts={facts.get(text)} /> : undefined} comboboxProps={{ width: 'max-content' }}
          renderOption={({ option }) => <OptionLine label={option.label} facts={facts.get(option.value)} />}
          onChange={(v) => { setText(v ?? ''); if (v) onSend(v); }} data-options />
      ) : (
        <TextInput size="xs" autoFocus placeholder={act.input && HINTS[act.input] ? t(HINTS[act.input]) : ''} value={text}
          onChange={(e) => setText(e.currentTarget.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' && text.trim()) onSend(text); }} />
      )}
      {/* A pick sends at once; a typed value waits for Send. */}
      {!act.options && <Button size="xs" loading={busy} disabled={!text.trim()} onClick={() => onSend(text)}>{t('Send')}</Button>}
      <Button size="xs" variant="subtle" onClick={onCancel}>{t('Cancel')}</Button>
    </Group>
  );
}
