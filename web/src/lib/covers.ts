import { useEffect, useState } from 'react';
import type { Menu } from '../hooks/useMenu';
import { fetchFile } from './api';

// The covers read in this tab, by their address: a file of an id never changes, so each is read once.
const read = new Map<string, Promise<string>>();
const ready = new Map<string, string>();

function cover(url: string): Promise<string> {
  let p = read.get(url);
  if (!p) {
    p = fetchFile(url).then((b) => {
      const src = URL.createObjectURL(b);
      ready.set(url, src);
      return src;
    });
    p.catch(() => read.delete(url));
    read.set(url, p);
  }
  return p;
}

// preload reads the covers of the tiles as soon as the menu names them.
export function preload(menu: Menu) {
  for (const p of menu.projects) {
    for (const url of [p.cover, p.cover_dark]) if (url) cover(url).catch(() => undefined);
  }
}

// FILE is the address of a file of deck, a stored one, one of a data folder or a picture deck ships with a theme: a
// cover is read with the token from nowhere else.
const FILE = /^\/api\/files\/(\d+|data\/[^?#]+(\?thumb=1)?|theme\/[a-z0-9-]+\/[a-z]+-(light|dark)\.webp)$/;

// useCover is the address of a cover for the page once it is read.
export function useCover(at?: string): string | undefined {
  const url = at && FILE.test(at) ? at : undefined;
  const [src, setSrc] = useState(() => (url ? ready.get(url) : undefined));
  useEffect(() => {
    if (!url) return;
    let gone = false;
    cover(url).then((s) => { if (!gone) setSrc(s); }, () => undefined);
    return () => { gone = true; };
  }, [url]);
  return url ? (ready.get(url) ?? src) : undefined;
}
