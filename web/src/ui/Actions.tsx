import { useId } from 'react';
import { ActionIcon, Group, Menu, Stack } from '@mantine/core';
import { IconDots } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Action } from '../lib/catalog.gen';
import { ActionButton } from './ActionButton';
import type { Before } from './useActionRun';
import { useVisibleActions } from './useVisibleActions';

// Actions are the buttons of an item, the primary first; past max the rest wait in a menu. before runs ahead of
// each call, onDone after one went through.
export function Actions({ actions, onDone, before, max = 3 }: { actions?: Action[]; onDone?: () => void; before?: Before; max?: number }) {
  const { t } = useTranslation();
  const more = useId();
  const shown = useVisibleActions(actions);
  if (!shown.length) return null;
  const sorted = [...shown.filter((a) => a.primary), ...shown.filter((a) => !a.primary)];
  const cut = sorted.length > max ? max - 1 : sorted.length;
  const rest = sorted.slice(cut);
  return (
    <Group gap={6} wrap="wrap" data-actions>
      {sorted.slice(0, cut).map((a, i) => <ActionButton key={i} act={a} onDone={onDone} before={before} />)}
      {rest.length > 0 && (
        <Menu position="bottom-end" keepMounted closeOnItemClick={false} withinPortal shadow="md" radius="md">
          <Menu.Target>
            <ActionIcon variant="default" size={30} radius="md" aria-label={t('actions.more')} data-more={more}><IconDots size={16} /></ActionIcon>
          </Menu.Target>
          <Menu.Dropdown p={4} miw={180} data-more-menu={more}>
            <Stack gap={2}>
              {rest.map((a, i) => <ActionButton key={i} act={a} onDone={onDone} before={before} inMenu />)}
            </Stack>
          </Menu.Dropdown>
        </Menu>
      )}
    </Group>
  );
}
