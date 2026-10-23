import { Text, ThemeIcon, UnstyledButton } from '@mantine/core';
import type { Action } from '../../lib/catalog.gen';
import { usePress } from '../../ui/actions/usePress';
import { NamedIcon } from '../../ui/NamedIcon';
import type { Item } from './kinds';
import s from './files.module.css';

const none: Action = {};

// Place is a line that is no file, such as a folder or a document: its icon, as a tile or a row, presses the
// primary button of the line.
export function Place({ it, size, row }: { it: Item; size: number; row?: boolean }) {
  const act = it.actions?.find((a) => a.primary);
  const { send, busy } = usePress(act ?? none);
  const name = it.title || it.name || '';
  return (
    <UnstyledButton className={row ? s.row : s.tile} style={row ? { flex: 1, minWidth: 0 } : undefined} disabled={!act || busy}
      onClick={() => void send()} data-item={row ? undefined : true} data-tile={row ? undefined : true} data-place={it.icon}>
      <ThemeIcon variant="light" size={size} radius="md" aria-hidden>
        <NamedIcon name={it.icon} fallback={name} size={Math.round(size * 0.55)} />
      </ThemeIcon>
      <Text size={row ? 'sm' : 'xs'} lineClamp={row ? 1 : 2} ta={row ? undefined : 'center'} style={row ? { flex: 1 } : { overflowWrap: 'anywhere' }}>{name}</Text>
    </UnstyledButton>
  );
}
