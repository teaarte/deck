import { memo, useEffect } from 'react';
import { Collapse, ScrollArea, Tooltip, UnstyledButton } from '@mantine/core';
import { useLocalStorage } from '@mantine/hooks';
import { IconChevronRight } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import { NamedIcon } from '../ui/NamedIcon';
import s from './nav.module.css';
import { NavStatus } from './NavStatus';
import { href } from './router';

// Nav lists deck's screens and every project's, each project a group that folds; a move to a screen unfolds its group.
export const Nav = memo(function Nav({ menu, current, onPick }: { menu: MenuEntry[]; current: string; onPick?: () => void }) {
  const { t } = useTranslation();
  const [folded, setFolded] = useLocalStorage<string[]>({ key: 'deck-nav-folded', defaultValue: [] });
  const here = menu.find((m) => [...m.screens, ...(m.extra ?? [])].some((x) => href({ project: m.project, screen: x.name }) === current));
  const hereKey = here ? here.project ?? '' : undefined;
  useEffect(() => {
    if (hereKey !== undefined && folded.includes(hereKey)) setFolded(folded.filter((x) => x !== hereKey));
  }, [current, hereKey]);
  return (
    <ScrollArea className={s.navList} scrollbarSize={6} type="hover">
      {menu.map((m) => {
        const key = m.project ?? '';
        const open = !folded.includes(key) || m.screens.length === 0;
        return (
          <div key={key} className={s.group} data-group={key}>
            <UnstyledButton className={s.groupHead} aria-expanded={open} data-group-head
              onClick={() => setFolded(open ? [...folded.filter((x) => x !== key), key] : folded.filter((x) => x !== key))}>
              <span className={s.groupIcon}><NamedIcon name={m.icon ?? (m.project ? undefined : 'layout-grid')} fallback={m.title} size={16} /></span>
              <span className={s.groupTitle}>{m.project ? m.title : t('nav.deck')}</span>
              <NavStatus m={m} />
              {m.screens.length > 0 && <IconChevronRight size={14} className={s.chevron} data-open={open || undefined} />}
            </UnstyledButton>
            {!!m.extra?.length && (
              <span className={s.extras}>
                {m.extra.map((x) => {
                  const to = href({ project: m.project, screen: x.name });
                  return (
                    <Tooltip key={x.name} label={x.title}>
                      <a href={to} className={s.extra} data-active={to === current || undefined} aria-label={x.title} data-extra={x.name} onClick={onPick}>
                        <NamedIcon name={x.icon} fallback={x.title} size={15} />
                      </a>
                    </Tooltip>
                  );
                })}
              </span>
            )}
            <Collapse expanded={open}>
              {m.screens.map((x) => {
                const to = href({ project: m.project, screen: x.name });
                return (
                  <a key={x.name} href={to} className={s.link} data-active={to === current || undefined} onClick={onPick}>
                    <NamedIcon name={x.icon ?? 'point'} fallback={x.title} size={17} />
                    <span className={s.linkTitle}>{x.title}</span>
                  </a>
                );
              })}
            </Collapse>
          </div>
        );
      })}
    </ScrollArea>
  );
});
