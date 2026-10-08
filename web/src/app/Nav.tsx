import { memo, useEffect } from 'react';
import { Collapse, Menu, ScrollArea, Tooltip, UnstyledButton } from '@mantine/core';
import { useLocalStorage } from '@mantine/hooks';
import { IconChevronRight, IconSelector } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import { NamedIcon } from '../ui/NamedIcon';
import s from './nav.module.css';
import { NavStatus } from './NavStatus';
import { href } from './router';

const SYSTEM = 'master';

const links = (m: MenuEntry) => [...m.screens, ...(m.extra ?? [])];

// System is deck's screens and the master's behind one button at the foot of the menu, apart from the projects.
function System({ menu, current, onPick }: { menu: MenuEntry[]; current: string; onPick?: () => void }) {
  const { t } = useTranslation();
  const path = current.split('?')[0];
  const here = menu.flatMap((m) => links(m).map((x) => ({ m, x }))).find(({ m, x }) => href({ project: m.project, screen: x.name }) === path);
  const master = menu.find((m) => m.project === SYSTEM);
  return (
    <Menu position="top-start" width="target" withinPortal shadow="md">
      <Menu.Target>
        <UnstyledButton className={s.system} data-active={here ? true : undefined} data-system>
          <span className={s.groupIcon}><NamedIcon name={here?.x.icon ?? 'layout-grid'} fallback={here?.x.title ?? 'deck'} size={16} /></span>
          <span className={s.groupTitle}>{here ? `${here.m.project ? here.m.title : t('nav.deck')} · ${here.x.title}` : t('nav.deck')}</span>
          {master && <NavStatus m={master} />}
          <IconSelector size={14} className={s.chevron} />
        </UnstyledButton>
      </Menu.Target>
      <Menu.Dropdown data-system-menu>
        {menu.map((m, i) => (
          <div key={m.project ?? ''} data-group={m.project ?? ''}>
            {i > 0 && <Menu.Divider />}
            <Menu.Label>{m.project ? m.title : t('nav.deck')}</Menu.Label>
            {links(m).map((x) => {
              const to = href({ project: m.project, screen: x.name });
              return (
                <Menu.Item key={x.name} component="a" href={to} onClick={onPick} data-active={to === path || undefined}
                  leftSection={<NamedIcon name={x.icon ?? 'point'} fallback={x.title} size={16} />}>
                  {x.title}
                </Menu.Item>
              );
            })}
          </div>
        ))}
      </Menu.Dropdown>
    </Menu>
  );
}

// Nav lists every project's screens, each project a group that folds, and deck's own with the master's below them;
// a move to a screen unfolds its group.
export const Nav = memo(function Nav({ menu: all, current, onPick }: { menu: MenuEntry[]; current: string; onPick?: () => void }) {
  const [folded, setFolded] = useLocalStorage<string[]>({ key: 'deck-nav-folded', defaultValue: [] });
  const system = all.filter((m) => !m.project || m.project === SYSTEM);
  const menu = all.filter((m) => m.project && m.project !== SYSTEM);
  const here = menu.find((m) => [...m.screens, ...(m.extra ?? [])].some((x) => href({ project: m.project, screen: x.name }) === current));
  const hereKey = here ? here.project ?? '' : undefined;
  useEffect(() => {
    if (hereKey !== undefined && folded.includes(hereKey)) setFolded(folded.filter((x) => x !== hereKey));
  }, [current, hereKey]);
  return (
    <>
      <ScrollArea className={s.navList} scrollbarSize={6} type="hover">
        {menu.map((m) => {
          const key = m.project ?? '';
          const open = !folded.includes(key) || m.screens.length === 0;
          return (
            <div key={key} className={s.group} data-group={key}>
              <UnstyledButton className={s.groupHead} aria-expanded={open} data-group-head
                onClick={() => setFolded(open ? [...folded.filter((x) => x !== key), key] : folded.filter((x) => x !== key))}>
                <span className={s.groupIcon}><NamedIcon name={m.icon} fallback={m.title} size={16} /></span>
                <span className={s.groupTitle}>{m.title}</span>
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
      {system.length > 0 && <div className={s.foot}><System menu={system} current={current} onPick={onPick} /></div>}
    </>
  );
});
