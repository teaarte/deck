import { useTranslation } from 'react-i18next';
import { useLook } from '../theme/look';
import s from './deki.module.css';

// Mood is the pose of Deki: dots while the master answers, wait while something waits for the owner, work while
// processes run, think over failing checks, chill when all is calm, sleepy when calm without the sky.
export type Mood = 'dots' | 'think' | 'wait' | 'work' | 'chill' | 'sleepy';

// useMascot says whether this device shows Deki.
export const useMascot = () => useLook().mascot;

// useMasterName is what the master is called: Deki while the mascot is on.
export function useMasterName() {
  const { t } = useTranslation();
  return useMascot() ? t('chat.deki') : t('chat.master');
}

// Deki is the mascot in a pose, size pixels high; float lets it hover.
export function Deki({ mood, size, float }: { mood: Mood; size: number; float?: boolean }) {
  if (!useMascot()) return null;
  return <img className={s.pose} src={`/deki/${mood}.webp`} alt="" height={size} style={{ height: size }} data-float={float || undefined} data-deki={mood} />;
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
