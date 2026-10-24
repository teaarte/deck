import { useEffect, useState } from 'react';
import { loadIcons, svgOf, type Nodes } from '../lib/icons';
import { useWorn } from '../theme/worn';
import s from './Backdrop.module.css';

const CELL = 72;
const CELLS = 10;

// rng is a seeded random, so the pattern is the same on every load.
function rng(seed: number) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// patterns are two tiles of random icons of the library on a grid, tilted, some cells empty; the set changes daily.
function patterns(all: Nodes, seed: number): [string, string] {
  const names = Object.keys(all).filter((n) => !/^(brand|letter|number|square-letter|circle-letter|square-number|circle-number)-|-off$/.test(n));
  const next = rng(seed);
  const body = ['', ''];
  for (let y = 0; y < CELLS; y++) {
    for (let x = 0; x < CELLS; x++) {
      if (next() < 0.3) continue;
      const name = names[Math.floor(next() * names.length)];
      const turn = Math.round((next() - 0.5) * 50);
      body[next() < 0.35 ? 1 : 0] += `<g transform="translate(${x * CELL + 24} ${y * CELL + 24}) rotate(${turn} 12 12)">${svgOf(all[name])}</g>`;
    }
  }
  const size = CELL * CELLS;
  const tile = (b: string) => `url("data:image/svg+xml;utf8,${encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}">` +
    `<g fill="none" stroke="black" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">${b}</g></svg>`)}")`;
  return [tile(body[0]), tile(body[1])];
}

// Backdrop is the canvas behind the shell as the theme has it: a grid of icons in the accent of the device, a sky with
// clouds, or the canvas alone.
export function Backdrop() {
  const { backdrop } = useWorn();
  const pattern = backdrop === 'pattern';
  const sky = backdrop === 'sky';
  const [tiles, setTiles] = useState<[string, string]>();
  useEffect(() => {
    if (!pattern || tiles || matchMedia('(prefers-contrast: more)').matches) return;
    loadIcons().then((all) => setTiles(patterns(all, Math.floor(Date.now() / 86400000))), () => undefined);
  }, [pattern]);
  return (
    <div className={s.backdrop} data-backdrop aria-hidden>
      {sky && <img className={`${s.cloud} ${s.cloudLeft}`} src="/deki/clouds-left.webp" alt="" data-cloud />}
      {sky && <img className={`${s.cloud} ${s.cloudRight}`} src="/deki/clouds-right.webp" alt="" data-cloud />}
      {pattern && tiles && <div className={s.pattern} style={{ maskImage: tiles[0], WebkitMaskImage: tiles[0] }} />}
      {pattern && tiles && <div className={`${s.pattern} ${s.patternAlt}`} style={{ maskImage: tiles[1], WebkitMaskImage: tiles[1] }} />}
    </div>
  );
}
