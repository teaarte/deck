import { useEffect, useState } from 'react';
import { ActionIcon, Button, Group, Indicator, Loader, Popover, ScrollArea, Stack, Text, UnstyledButton } from '@mantine/core';
import { IconArrowUpRight, IconBell, IconBellOff } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import i18n from '../lib/i18n';
import { useMenuData } from '../hooks/useMenu';
import { useLiveRead } from '../hooks/useLiveRead';
import { formatted } from '../lib/format';
import { act } from '../screen/act';
import { titleCount } from './title';
import s from './bell.module.css';
import type { Action } from '../lib/catalog.gen';
import { Actions } from '../ui/actions/Actions';

interface Note {
  id: number;
  project: string;
  text: string;
  buttons?: Action[];
  read: boolean;
  created_at: string;
  // open opens the screen of the note, mark_read marks it read; deck gives each when it has one.
  open?: Action;
  mark_read?: Action;
}

// Bell holds the notifications of deck: the count of the notes not read, as the cockpit counts them, and their list,
// read while it is open, each with its buttons.
export function Bell() {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const fresh = useMenuData().pulse.notes ?? 0;
  const notes = useLiveRead<Note[] | undefined>('deck.feed_list', { kind: 'note', limit: 30, lang: i18n.language }, ['feed'], undefined, !open);
  useEffect(() => titleCount(fresh), [fresh]);
  const openNote = (n: Note) => { setOpen(false); if (n.open) void act(n.open); };
  const markRead = (n: Note) => { if (n.mark_read) act(n.mark_read).catch(() => undefined); };
  const toggle = () => setOpen(!open);
  return (
    <Popover opened={open} onChange={setOpen} position="bottom-start" width={340} shadow="lg" radius="lg" withinPortal>
      <Popover.Target>
        <Indicator label={fresh} size={16} offset={4} disabled={!fresh} color="red" data-bell-count={fresh || undefined}>
          <ActionIcon variant="subtle" color="gray" radius="xl" onClick={toggle} aria-label={t('bell.title')} data-bell>
            <IconBell size={18} />
          </ActionIcon>
        </Indicator>
      </Popover.Target>
      <Popover.Dropdown p={0} data-bell-list>
        <Text fw={700} px="md" pt="sm" pb={6}>{t('bell.title')}</Text>
        {!notes ? (
          <Group px="md" pb="md"><Loader size="xs" /></Group>
        ) : !notes.length ? (
          <Group gap={8} c="dimmed" px="md" pb="md"><IconBellOff size={16} /><Text size="sm" c="dimmed">{t('bell.empty')}</Text></Group>
        ) : (
          <ScrollArea.Autosize mah={420} type="hover">
            <Stack gap={0} pb={6}>
              {notes.map((n) => (
                <div key={n.id} className={s.note} data-note={n.id} data-fresh={!n.read || undefined} data-read={n.read || undefined}>
                  <UnstyledButton disabled={!n.open} onClick={() => openNote(n)} className={s.noteBody}>
                    <Text size="sm" lh={1.4}>{n.text}</Text>
                    <Text size="xs" c="dimmed" mt={2}>{n.project} · {formatted(n.created_at, { format: 'relative' })}</Text>
                  </UnstyledButton>
                  {(!!n.buttons?.length || n.mark_read || n.open) && (
                    <Group gap={6} className={s.noteActions}>
                      {n.open && <Button size="compact-xs" variant="light" leftSection={<IconArrowUpRight size={13} />} onClick={() => openNote(n)} data-note-open>{t('verb.open')}</Button>}
                      {!!n.buttons?.length && <Actions actions={n.buttons} />}
                      {n.mark_read && <Button size="compact-xs" variant="subtle" onClick={() => markRead(n)} data-note-read>{t('verb.mark_read')}</Button>}
                    </Group>
                  )}
                </div>
              ))}
            </Stack>
          </ScrollArea.Autosize>
        )}
      </Popover.Dropdown>
    </Popover>
  );
}
