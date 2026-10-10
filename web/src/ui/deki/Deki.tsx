import type { CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import { useLook } from '../../theme/look';
import s from './deki.module.css';

// Mood is the pose of Deki: wait while something waits for the owner, work at the phone while anything runs, dots
// while a turn of the chat goes, think over failing checks, chill when all is calm, sleepy when calm without the sky.
export type Mood = 'dots' | 'think' | 'wait' | 'work' | 'chill' | 'sleepy';

// useMascot says whether this device shows Deki.
export const useMascot = () => useLook().mascot;

// useMasterName is what the master is called: Deki while the mascot is on.
export function useMasterName() {
  const { t } = useTranslation();
  return useMascot() ? t('chat.deki') : t('chat.master');
}

// Deki is the mascot in a pose, size pixels high, moving the way the pose moves; still keeps it in place.
export function Deki({ mood, size, still }: { mood: Mood; size: number; still?: boolean }) {
  if (!useMascot()) return null;
  return (
    <span key={mood} className={s.pose} style={{ '--deki-src': `url(/deki/${mood}.webp)` } as CSSProperties} data-still={still || undefined} data-deki={mood}>
      <img src={`/deki/${mood}.webp`} alt="" height={size} style={{ height: size }} />
    </span>
  );
}

// DekiHead is the face of Deki in a round or rounded square, size pixels across; bob lets it sway.
export function DekiHead({ size, square, bob }: { size: number; square?: boolean; bob?: boolean }) {
  if (!useMascot()) return null;
  return (
    <span className={s.head} style={{ width: size, height: size, borderRadius: square ? Math.round(size / 3) : '50%' }} data-bob={bob || undefined} data-deki="head">
      <img src="/deki/head.webp" alt="" />
    </span>
  );
}
