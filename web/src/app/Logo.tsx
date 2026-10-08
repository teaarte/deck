import { IconSparkles } from '@tabler/icons-react';
import s from './shell.module.css';
import { href } from './router';

// Logo is the mark and name of deck; it opens the cockpit.
export function Logo({ onClick }: { onClick?: () => void }) {
  return (
    <a className={s.logo} href={href({ screen: 'cockpit' })} onClick={onClick} data-logo>
      <span className={s.logoMark}><IconSparkles size={16} stroke={2} /></span>
      deck
    </a>
  );
}
