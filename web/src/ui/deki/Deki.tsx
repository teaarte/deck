import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useLook } from '../../theme/look';
import s from './deki.module.css';

// Pose is what Deki does, one for each state of deck it shows.
export type Pose = 'chill' | 'think' | 'work' | 'wait' | 'point' | 'worry' | 'done' | 'listen' | 'read' | 'draw' | 'search'
  | 'sleepy' | 'hello';

// POSES are the poses with a picture in /deki.
const POSES: Pose[] = ['chill', 'think', 'work', 'wait', 'point', 'worry', 'done', 'listen', 'read', 'draw', 'search', 'sleepy', 'hello'];

// The pictures load once, so a change of pose does not blink.
for (const p of POSES) new Image().src = `/deki/${p}.webp`;

// PULSE are the poses of the states of the pulse of deck; a calm one rests, or sleeps at night.
const PULSE: Record<string, Pose> = { asks: 'point', errors: 'worry', talking: 'think', running: 'work' };

// night is the hours Deki sleeps when nothing goes, by the clock of the device.
const night = (d = new Date()) => d.getHours() >= 23 || d.getHours() < 7;

// poseOf is the pose of Deki for a state of the pulse.
export const poseOf = (state: string): Pose => PULSE[state] ?? (night() ? 'sleepy' : 'chill');

// turnPose is the pose of a turn of the chat: thinking before its first call, then by the kind of work of the call it
// makes, waiting while the owner owes it an answer.
export function turnPose(waiting: boolean, act?: string): Pose {
  if (waiting) return 'wait';
  if (!act) return 'think';
  return act === 'read' || act === 'search' || act === 'draw' ? act : 'work';
}

// useMascot says whether this device shows Deki.
export const useMascot = () => useLook().mascot;

// useMasterName is what the master is called: Deki while the mascot is on.
export function useMasterName() {
  const { t } = useTranslation();
  return useMascot() ? t('chat.deki') : t('chat.master');
}

// still is the motion off: by the owner on this device or by the system.
const stillAll = (motion: boolean) => !motion || matchMedia('(prefers-reduced-motion: reduce)').matches;

// Picture is one pose drawn, moving the way it moves unless still; one leaving fades out under the next. Its motion
// plays again at each wake and each new pose, without the picture coming in again.
function Picture({ pose, size, still, leaving, wake = 0 }: { pose: Pose; size: number; still?: boolean; leaving?: boolean; wake?: number }) {
  const ref = useRef<HTMLSpanElement>(null);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    const el = ref.current;
    if (!el) return;
    el.style.animation = 'none';
    void el.offsetWidth;
    el.style.animation = '';
  }, [wake, pose]);
  const src = `/deki/${pose}.webp`;
  return (
    <span ref={ref} className={s.pose} data-still={still || undefined}
      data-leaving={leaving || undefined} data-pose={pose} data-deki={leaving ? undefined : pose}>
      <img src={src} alt="" height={size} style={{ height: size }} />
    </span>
  );
}

// Deki is the mascot in a pose, size pixels high. A new picture comes in over the one before as it fades; its motion
// plays a few times and rests, again at each wake.
export function Deki({ pose, size, still, wake = 0 }: { pose: Pose; size: number; still?: boolean; wake?: number }) {
  const mascot = useMascot();
  const { motion } = useLook();
  const last = useRef(pose);
  const [was, setWas] = useState<Pose>();
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => {
    if (last.current === pose) return;
    const before = last.current;
    last.current = pose;
    clearTimeout(timer.current);
    if (before === pose || stillAll(motion)) {
      setWas(undefined);
      return;
    }
    setWas(before);
    timer.current = setTimeout(() => setWas(undefined), 400);
  }, [pose, motion]);
  useEffect(() => () => clearTimeout(timer.current), []);
  if (!mascot) return null;
  return (
    <span className={s.deki}>
      {was && <Picture pose={was} size={size} still leaving />}
      <Picture key={pose} pose={pose} size={size} still={still} wake={wake} />
    </span>
  );
}

// DekiHead is the face of Deki in a round or rounded square, size pixels across.
export function DekiHead({ size, square }: { size: number; square?: boolean }) {
  if (!useMascot()) return null;
  return (
    <span className={s.head} style={{ width: size, height: size, borderRadius: `min(${square ? Math.round(size / 3) + 'px' : '50%'}, calc(var(--mantine-radius-default) * 100))` }} data-deki="head">
      <img src="/deki/avatar.webp" alt="" />
    </span>
  );
}
