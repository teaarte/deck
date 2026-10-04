import { IconSparkles } from '@tabler/icons-react';
import s from '../shell.module.css';

export function Logo() {
  return (
    <span className={s.logo}>
      <span className={s.logoMark}><IconSparkles size={16} stroke={2} /></span>
      deck
    </span>
  );
}
