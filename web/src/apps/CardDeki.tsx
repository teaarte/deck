import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { setChatOpen } from '../chat/chatStore';
import { useMenuData } from '../hooks/useMenu';
import { useLook } from '../theme/look';
import { Deki, poseOf, useMascot, useMasterName, type Pose } from '../ui/deki/Deki';
import s from './apps.module.css';

// MOMENT is how long Deki greets on the first open of a device, or is glad when the work ran out.
const MOMENT = 5000;

// usePose is the pose of the pulse, but for a moment: hello on the first open of the device, done when what ran
// ended with nothing left to decide or fix.
function usePose(state: string): Pose {
  const [moment, setMoment] = useState<Pose | undefined>(() => (localStorage.getItem('deck-deki-hello') ? undefined : 'hello'));
  const was = useRef(state);
  useEffect(() => {
    localStorage.setItem('deck-deki-hello', '1');
    const busy = was.current === 'running' || was.current === 'talking';
    was.current = state;
    if (busy && state === 'calm') setMoment('done');
  }, [state]);
  useEffect(() => {
    if (!moment) return;
    const t = setTimeout(() => setMoment(undefined), MOMENT);
    return () => clearTimeout(t);
  }, [moment]);
  return moment ?? poseOf(state);
}

// CardDeki is Deki on the cockpit's card in the pose of the pulse, turning to the pointer over the card and waking
// as it comes; a press opens the chat with the master.
export function CardDeki({ size }: { size: number }) {
  const { t } = useTranslation();
  const { pulse } = useMenuData();
  const { motion } = useLook();
  const pose = usePose(pulse.state);
  const [wake, setWake] = useState(0);
  const ref = useRef<HTMLButtonElement>(null);
  const mascot = useMascot();
  const name = useMasterName();
  useEffect(() => {
    const el = ref.current;
    const card = el?.parentElement;
    if (!el || !card || !motion || !matchMedia('(hover: hover) and (pointer: fine)').matches) return;
    let frame = 0;
    const move = (e: PointerEvent) => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const r = el.getBoundingClientRect();
        const x = Math.max(-1, Math.min(1, (e.clientX - (r.left + r.width / 2)) / 300));
        const y = Math.max(-1, Math.min(1, (e.clientY - (r.top + r.height / 2)) / 300));
        el.style.setProperty('--look-x', `${(x * 6).toFixed(1)}px`);
        el.style.setProperty('--look-y', `${(y * 3).toFixed(1)}px`);
        el.style.setProperty('--look-r', `${(x * 4).toFixed(1)}deg`);
      });
    };
    const leave = () => {
      cancelAnimationFrame(frame);
      ['--look-x', '--look-y', '--look-r'].forEach((k) => el.style.removeProperty(k));
    };
    const come = () => setWake((n) => n + 1);
    card.addEventListener('pointermove', move);
    card.addEventListener('pointerleave', leave);
    card.addEventListener('pointerenter', come);
    return () => {
      leave();
      card.removeEventListener('pointermove', move);
      card.removeEventListener('pointerleave', leave);
      card.removeEventListener('pointerenter', come);
    };
  }, [motion, mascot]);
  if (!mascot) return null;
  return (
    <button ref={ref} type="button" className={s.deki} onClick={() => setChatOpen(true)} aria-label={t('chat.talk', { name })} data-card-deki={pulse.state}>
      <Deki pose={pose} size={size} still={!motion} wake={wake} />
    </button>
  );
}
