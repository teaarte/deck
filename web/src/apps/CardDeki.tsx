import { useEffect, useRef, type CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import { setChatOpen } from '../chat/chatStore';
import { useMenuData } from '../hooks/useMenu';
import { useLook } from '../theme/look';
import { useWorn } from '../theme/worn';
import { Deki, moodOf, useMascot, useMasterName } from '../ui/deki/Deki';
import s from './apps.module.css';

// CardDeki is Deki on the cockpit's card in the pose of the pulse, turning to the pointer over the card; a press
// opens the chat with the master.
export function CardDeki({ size }: { size: number }) {
  const { t } = useTranslation();
  const { pulse } = useMenuData();
  const { motion } = useLook();
  const sky = useWorn().backdrop === 'sky';
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
    card.addEventListener('pointermove', move);
    card.addEventListener('pointerleave', leave);
    return () => { leave(); card.removeEventListener('pointermove', move); card.removeEventListener('pointerleave', leave); };
  }, [motion, mascot]);
  if (!mascot) return null;
  return (
    <button ref={ref} type="button" className={s.deki} onClick={() => setChatOpen(true)} aria-label={t('chat.talk', { name })} data-card-deki={pulse.state}>
      <Deki mood={moodOf(pulse.state, sky)} size={size} still={!motion} />
    </button>
  );
}
