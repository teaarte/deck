import { useId, type ReactNode } from 'react';
import { ActionIcon, Button, Menu, Stack } from '@mantine/core';
import { IconChevronDown, IconDots } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Action } from '../../lib/catalog.gen';
import { ActionButton } from './ActionButton';
import { actionKeys, useVisibleActions } from './useVisibleActions';

// ActionsMenu is one button that opens buttons in a menu: a label with its icon, or dots without one.
export function ActionsMenu({ actions, label, icon, onDone }: { actions?: Action[]; label?: string; icon?: ReactNode; onDone?: () => void }) {
  const { t } = useTranslation();
  const id = useId();
  const shown = useVisibleActions(actions);
  if (!shown.length) return null;
  const keys = actionKeys(shown);
  return (
    <Menu position="bottom-end" keepMounted closeOnItemClick={false} withinPortal shadow="md" radius="md">
      <Menu.Target>
        {label ? (
          <Button variant="default" radius="xl" leftSection={icon} rightSection={<IconChevronDown size={14} />} data-more={id} data-menu>{label}</Button>
        ) : (
          <ActionIcon variant="subtle" color="gray" radius="xl" aria-label={t('actions.more')} data-more={id} data-menu><IconDots size={18} /></ActionIcon>
        )}
      </Menu.Target>
      <Menu.Dropdown p={4} miw={190} data-more-menu={id}>
        <Stack gap={2}>{shown.map((a, i) => <ActionButton key={keys[i]} act={a} onDone={onDone} inMenu />)}</Stack>
      </Menu.Dropdown>
    </Menu>
  );
}
