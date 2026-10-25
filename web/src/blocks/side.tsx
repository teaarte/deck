import { createContext, useCallback, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Badge, Button, Drawer, Stack } from '@mantine/core';
import { IconLayoutSidebarLeftExpand } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { usePhone } from '../hooks/usePhone';
import { Small } from './around';
import s from './blocks.module.css';

// Picks hears each filter of the side panel say whether its param is picked, so the phone's button counts them.
export const Picks = createContext<(param: string, on: boolean) => void>(() => {});

// Side is the side panel of a screen: beside its blocks on a computer, in a sheet a button in the row of the tabs
// opens on a phone, the button in the head of the screen when the frame has no row.
export function Side({ blocks, slot, children }: { blocks: ReactNode; slot?: HTMLElement | null; children: ReactNode }) {
  const { t } = useTranslation();
  const phone = usePhone();
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
  const mark = useCallback((param: string, on: boolean) => setPicked((was) => {
    if (was.has(param) === on) return was;
    const next = new Set(was);
    if (on) next.add(param);
    else next.delete(param);
    return next;
  }), []);
  const panel = <Small.Provider value><Stack gap="md">{blocks}</Stack></Small.Provider>;
  if (!phone) {
    return (
      <Picks.Provider value={mark}>
        <div className={s.withSide} data-with-side>
          <aside className={s.side} data-side aria-label={t('side.title')}>{panel}</aside>
          <div className={s.sideMain}>{children}</div>
        </div>
      </Picks.Provider>
    );
  }
  const button = (
    <Button size="compact-sm" variant="default" leftSection={<IconLayoutSidebarLeftExpand size={15} />} onClick={() => setOpen(true)} data-side-open
      rightSection={picked.size > 0 ? <Badge size="xs" circle>{picked.size}</Badge> : undefined} aria-label={t('side.title')}>
      {t('side.open')}
    </Button>
  );
  return (
    <Picks.Provider value={mark}>
      {slot ? createPortal(button, slot) : <div className={s.sideButton}>{button}</div>}
      {children}
      <Drawer opened={open} onClose={() => setOpen(false)} position="bottom" size="auto" title={t('side.title')} keepMounted data-side-sheet
        classNames={{ content: s.sideSheet }}>
        <div data-side>{panel}</div>
      </Drawer>
    </Picks.Provider>
  );
}
