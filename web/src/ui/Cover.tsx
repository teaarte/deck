import { useState } from 'react';
import { useComputedColorScheme } from '@mantine/core';
import type { Picture } from '../lib/catalog.gen';
import { useCover } from '../lib/covers';
import s from './cover.module.css';

// Cover is the light copy of the picture of a card or a folder tile in the scheme of the device, its main color
// while it loads.
export function Cover({ cover, className }: { cover: Picture; className?: string }) {
  const dark = useComputedColorScheme('light') === 'dark';
  const url = dark ? cover.dark || cover.light : cover.light;
  const color = dark ? cover.dark_color || cover.color : cover.color;
  const src = useCover(url);
  const [shown, setShown] = useState(false);
  return (
    <span className={[s.cover, className].filter(Boolean).join(' ')} style={color ? { backgroundColor: color } : undefined} data-cover>
      {src && <img src={src} alt="" className={s.img} data-shown={shown || undefined} onLoad={() => setShown(true)} />}
    </span>
  );
}
