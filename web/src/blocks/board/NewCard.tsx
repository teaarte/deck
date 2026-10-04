import { useState } from 'react';
import { TextInput } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { call } from '../../lib/api';
import { notifyError } from '../../lib/notify';

// NewCard adds a card to a lane by its title.
export function NewCard({ project, lane }: { project: string; lane: string }) {
  const { t } = useTranslation();
  const [title, setTitle] = useState('');
  const [busy, setBusy] = useState(false);
  const add = async () => {
    if (!title.trim() || busy) return;
    setBusy(true);
    try {
      await call('deck.card_create', { project, lane, title: title.trim() });
      setTitle('');
    } catch (e) {
      notifyError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <TextInput size="xs" placeholder={t('board.new')} value={title} disabled={busy} data-new-card
      onChange={(e) => setTitle(e.currentTarget.value)} onKeyDown={(e) => { if (e.key === 'Enter') void add(); }} />
  );
}
