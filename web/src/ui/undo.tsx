import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Button, Group, Text } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import i18n from '../lib/i18n';
import type { Action } from '../lib/catalog.gen';
import { dispatch } from './dispatch';

let undos = 0;

// offerUndo shows for a moment the call that takes back the one that just went through.
export function offerUndo(undo: Action, label: string, context: ComponentContext) {
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
