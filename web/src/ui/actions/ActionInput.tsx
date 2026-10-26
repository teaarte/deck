import { useState } from 'react';
import { Button, Group, Select, TextInput } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Action } from '../../lib/catalog.gen';

// choices are the options of a button for a select: each value with its label in the owner's language.
export const choices = (act: Action) => (act.options ?? []).map((value, i) => ({ value, label: act.labels?.[i] ?? value }));

// choiceData are the choices under the heading of their group, when the button gives the groups.
export function choiceData(act: Action) {
  const flat = choices(act);
  if (!act.groups) return flat;
  const out: { group: string; items: typeof flat }[] = [];
  flat.forEach((o, i) => {
    const group = act.groups![i] ?? '';
    if (out.at(-1)?.group !== group) out.push({ group, items: [] });
    out.at(-1)!.items.push(o);
  });
  return out;
}

// ActionInput takes the one value a button asks for, typed or picked from its options.
export function ActionInput({ act, busy, onSend, onCancel }: { act: Action; busy: boolean; onSend: (text: string) => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  return (
    <Group gap="xs" wrap="nowrap">
      {act.options ? (
        <Select size="xs" autoFocus placeholder={t('input.' + act.input, { defaultValue: '' })} data={choiceData(act)} value={text || null}
          onChange={(v) => setText(v ?? '')} data-options />
      ) : (
        <TextInput size="xs" autoFocus placeholder={t('input.' + act.input, { defaultValue: '' })} value={text}
          onChange={(e) => setText(e.currentTarget.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' && text.trim()) onSend(text); }} />
      )}
      <Button size="xs" loading={busy} disabled={!text.trim()} onClick={() => onSend(text)}>{t('verb.send')}</Button>
      <Button size="xs" variant="subtle" onClick={onCancel}>{t('verb.cancel')}</Button>
    </Group>
  );
}
