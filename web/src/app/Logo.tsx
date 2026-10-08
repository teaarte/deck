import { IconLayoutDashboard } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import s from './shell.module.css';
import { href } from './router';

// Logo is the way to the cockpit at the head of the menu: its icon and name.
export function Logo({ onClick }: { onClick?: () => void }) {
  const { t } = useTranslation();
  return (
    <a className={s.logo} href={href({ screen: 'cockpit' })} onClick={onClick} data-logo>
      <span className={s.logoMark}><IconLayoutDashboard size={17} stroke={2} /></span>
      {t('nav.cockpit')}
    </a>
  );
}
