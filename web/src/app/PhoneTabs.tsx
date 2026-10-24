import type { ReactNode } from 'react';
import { Menu, UnstyledButton } from '@mantine/core';
import { IconDots, IconLayoutGrid, IconSparkles, IconSun } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useMenuData, type Place } from '../hooks/useMenu';
import { useMasterName } from '../ui/deki/Deki';
import { NamedIcon } from '../ui/NamedIcon';
import { href, useRoute } from './router';
import s from './shell.module.css';

// Tab is one tab of the bar: a link or a button, its icon over its word.
function Tab({ to, icon, label, active, count, onClick, data }: {
  to?: string; icon: ReactNode; label: string; active?: boolean; count?: number; onClick?: () => void; data?: Record<string, string | boolean>;
}) {
  const body = (
    <>
      <span className={s.tabIcon}>
        {icon}
        {!!count && <span className={s.tabCount} aria-hidden>{count > 99 ? '99+' : count}</span>}
      </span>
      <span className={s.tabLabel}>{label}</span>
    </>
  );
  const said = count ? `${label}: ${count}` : label;
  return to
    ? <a href={to} className={s.tab} data-active={active || undefined} aria-current={active ? 'page' : undefined} aria-label={said} {...data}>{body}</a>
    : <UnstyledButton className={s.tab} data-active={active || undefined} aria-label={said} onClick={onClick} {...data}>{body}</UnstyledButton>;
}

// PhoneTabs are the tabs at the foot of a phone: Today with its decisions, the modules, Deki who opens the chat, the
// schedule, and the rest of deck's screens and the master's behind More.
export function PhoneTabs({ onChat }: { onChat: () => void }) {
  const { t } = useTranslation();
  const menu = useMenuData();
  const route = useRoute();
  const name = useMasterName();
  const links: Place[] = menu.system.flatMap((g) => g.links).filter((l) => !(l.project && l.project === menu.master?.project && l.name === 'settings'));
  const schedule = links.find((l) => !l.project && l.name === 'schedule');
  const more = links.filter((l) => l !== schedule);
  const here = (l: Place) => l.project === route.project && l.name === route.screen;
  const deck = !route.project;
  const inMore = more.some(here);
  return (
    <nav className={s.tabs} aria-label={t('nav.screens')} data-phone-tabs>
      <Tab to={href({ screen: 'cockpit' })} icon={<IconSun size={21} />} label={t('nav.cockpit')} count={menu.pulse.asks}
        active={deck && route.screen === 'cockpit'} data={{ 'data-tab': 'cockpit' }} />
      <Tab to={href({ screen: 'apps' })} icon={<IconLayoutGrid size={21} />} label={t('nav.apps')}
        active={(deck && route.screen === 'apps') || (!!route.project && route.project !== menu.master?.project)} data={{ 'data-tab': 'apps' }} />
      <Tab icon={<IconSparkles size={21} />} label={name} onClick={onChat} data={{ 'data-tab': 'deki', 'data-chat-toggle': true, 'data-deki': true }} />
      {schedule && (
        <Tab to={href({ screen: schedule.name })} icon={<NamedIcon name={schedule.icon} fallback={schedule.title} size={21} />} label={schedule.title}
          active={here(schedule)} data={{ 'data-tab': 'schedule' }} />
      )}
      <Menu position="top-end" withinPortal>
        <Menu.Target>
          <span className={s.tabWrap}>
            <Tab icon={<IconDots size={21} />} label={t('nav.more')} active={inMore} data={{ 'data-tab': 'more' }} />
          </span>
        </Menu.Target>
        <Menu.Dropdown data-more>
          {more.map((l) => (
            <Menu.Item key={(l.project ?? '') + l.name} component="a" href={href({ project: l.project, screen: l.name })}
              leftSection={<NamedIcon name={l.icon ?? 'point'} fallback={l.title} size={16} />} data-more-link={l.name}>
              {l.title}
            </Menu.Item>
          ))}
        </Menu.Dropdown>
      </Menu>
    </nav>
  );
}
