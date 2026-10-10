import { useCallback, useEffect, useState } from 'react';
import { ActionIcon, Button, Group, Indicator, Popover, ScrollArea, Stack, Text, UnstyledButton } from '@mantine/core';
import { IconArrowUpRight, IconBell, IconBellOff } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { call } from '../lib/api';
import { events } from '../lib/events';
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

const SEEN = 'deck-notes-seen';

// Bell holds the notifications of deck: a count of the new ones and their list, each with its buttons.
export function Bell() {
  const { t } = useTranslation();
  const [notes, setNotes] = useState<Note[]>([]);
  const [seen, setSeen] = useState(() => Number(localStorage.getItem(SEEN) ?? 0));
  const [open, setOpen] = useState(false);
  const load = useCallback(() => { call<Note[]>('deck.feed_list', { kind: 'note', limit: 30 }).then(setNotes, () => undefined); }, []);
  useEffect(() => {
    load();
    return events((e) => { if (e.topic === 'feed') load(); }, load);
  }, []);
  const fresh = notes.filter((n) => n.id > seen && !n.read).length;
  useEffect(() => titleCount(fresh), [fresh]);
  const openNote = (n: Note) => { setOpen(false); if (n.open) void act(n.open); };
  const markRead = (n: Note) => { if (n.mark_read) act(n.mark_read).then(load, () => undefined); };
  const toggle = () => {
    if (!open && notes.length) {
      const top = Math.max(...notes.map((n) => n.id));
      localStorage.setItem(SEEN, String(top));
      setSeen(top);
    }
    setOpen(!open);
  };
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
        {!notes.length ? (
          <Group gap={8} c="dimmed" px="md" pb="md"><IconBellOff size={16} /><Text size="sm" c="dimmed">{t('bell.empty')}</Text></Group>
        ) : (
          <ScrollArea.Autosize mah={420} type="hover">
            <Stack gap={0} pb={6}>
              {notes.map((n) => (
                <div key={n.id} className={s.note} data-note={n.id} data-fresh={(n.id > seen && !n.read) || undefined} data-read={n.read || undefined}>
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
