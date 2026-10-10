import { IconLayoutDashboard } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useMenuEntries } from '../hooks/useMenu';
import s from './shell.module.css';
import { href } from './router';

// waiting is how many decisions wait for the owner across the menu.
export const waiting = (menu: { asks?: number }[]) => menu.reduce((n, m) => n + (m.asks ?? 0), 0);

// Logo is the way to the cockpit at the head of the menu: its icon with what waits for the owner, and its name.
export function Logo({ onClick }: { onClick?: () => void }) {
  const { t } = useTranslation();
  const asks = waiting(useMenuEntries());
  return (
    <a className={s.logo} href={href({ screen: 'cockpit' })} onClick={onClick} data-logo>
      <span className={s.logoMark}>
        <IconLayoutDashboard size={17} stroke={2} />
        {asks > 0 && <span className={s.logoCount} data-cockpit-count={asks} aria-label={t('nav.asks', { n: asks })}>{asks > 99 ? '99+' : asks}</span>}
      </span>
      {t('nav.cockpit')}
    </a>
  );
}
