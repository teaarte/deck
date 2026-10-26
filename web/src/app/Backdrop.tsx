import { useComputedColorScheme } from '@mantine/core';
import { useCover } from '../lib/covers';
import { useWorn, type Picture } from '../theme/worn';
import s from './Backdrop.module.css';

// of is the address of a picture in a scheme: its own, else the other's.
export const of = (p: Picture | undefined, dark: boolean) => (dark ? p?.dark || p?.light : p?.light || p?.dark);

// Backdrop is the canvas behind the shell as the theme has it: the canvas, the theme's picture over it, wide or as a
// tile repeated, and its meadow at the foot.
export function Backdrop() {
  const { images } = useWorn();
  const dark = useComputedColorScheme('light') === 'dark';
  const wide = useCover(of(images?.backdrop, dark));
  const tile = useCover(of(images?.pattern, dark));
  return (
    <div className={s.backdrop} data-backdrop aria-hidden>
      <div className={s.meadow} data-meadow />
      {wide && <div className={s.picture} style={{ backgroundImage: `url("${wide}")` }} data-backdrop-picture />}
      {tile && <div className={`${s.picture} ${s.pattern}`} style={{ backgroundImage: `url("${tile}")` }} data-backdrop-pattern />}
    </div>
  );
}
