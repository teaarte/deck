import { useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Button, Group, Modal, Stack, TextInput, Textarea } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { call } from '../../lib/api';
import { notifyError } from '../../lib/notify';
import { Actions } from '../../ui/Actions';
import type { BoardCard } from './Card';
import { useChatCard } from './useChatCard';

// CardEditor edits the title and text of a card; its buttons save the edit before they run.
export function CardEditor({ card, context, onClose }: { card: BoardCard; context: ComponentContext; onClose: () => void }) {
  const { t } = useTranslation();
  useChatCard(card.id);
  const [title, setTitle] = useState(card.title);
  const [text, setText] = useState(card.text ?? '');
  const [busy, setBusy] = useState(false);
  const run = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
      onClose();
    } catch (e) {
      notifyError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal opened onClose={onClose} title={t('board.card', { id: card.id })}>
      <Stack gap="sm">
        <TextInput label={t('board.title')} value={title} onChange={(e) => setTitle(e.currentTarget.value)} />
        <Textarea label={t('board.text')} autosize minRows={3} value={text} onChange={(e) => setText(e.currentTarget.value)} />
        <Group justify="space-between" align="flex-start">
          <Actions actions={card.actions} context={context} onDone={onClose} before={async () => {
            if (!title.trim()) throw new Error(t('board.no_title'));
            if (title !== card.title || text !== (card.text ?? '')) await call('deck.card_update', { card: card.id, title, text });
          }} />
          <Button loading={busy} disabled={!title.trim()} onClick={() => run(() => call('deck.card_update', { card: card.id, title, text }))}>
            {t('board.save')}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
