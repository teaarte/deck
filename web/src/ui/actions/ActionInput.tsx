import { useState } from 'react';
import { Button, Group, Select, TextInput } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Action } from '../../lib/catalog.gen';

// ActionInput takes the one value a button asks for, typed or picked from its options.
export function ActionInput({ act, busy, onSend, onCancel }: { act: Action; busy: boolean; onSend: (text: string) => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  return (
    <Group gap="xs" wrap="nowrap">
      {act.options ? (
        <Select size="xs" autoFocus placeholder={t('input.' + act.input, { defaultValue: '' })} data={act.options} value={text || null}
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
