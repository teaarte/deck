import { token } from '../../lib/api';
import type { Data } from '../../lib/catalog.gen';

export type Item = NonNullable<Data['files']>[number];
export type Kind = 'image' | 'svg' | 'video' | 'audio' | 'pdf' | 'html' | 'text' | 'other';

// PICTURES are the types drawn as a picture: raster only, so no file runs as a script of deck.
const PICTURES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/avif', 'image/bmp'];
const TEXTS = ['json', 'xml', 'yaml', 'toml', 'csv', 'javascript', 'typescript', 'x-sh', 'sql', 'markdown'];
const TEXT_EXT = ['txt', 'md', 'json', 'jsonl', 'yaml', 'yml', 'toml', 'csv', 'tsv', 'log', 'xml', 'js', 'ts', 'tsx', 'jsx', 'py', 'go', 'rs',
  'sh', 'sql', 'css', 'ini', 'cfg', 'conf', 'srt', 'vtt'];
// PREVIEW is the largest file read whole for a preview; TEXT the most of a text read for one.
export const PREVIEW = 20 * 1000 * 1000;
export const TEXT = 2 << 20;

export const extOf = (name = '') => (/\.([a-z0-9]+)$/i.exec(name)?.[1] ?? '').toLowerCase();

// kindOf is how a file opens: by its type, or by its extension when the type says nothing.
export function kindOf(it: Item): Kind {
  const type = it.type ?? '';
  const ext = extOf(it.name);
  if (PICTURES.includes(type)) return 'image';
  if (type === 'image/svg+xml' || ext === 'svg') return 'svg';
  if (type.startsWith('video/')) return 'video';
  if (type.startsWith('audio/')) return 'audio';
  if (type === 'application/pdf' || ext === 'pdf') return 'pdf';
  if (type === 'text/html' || ext === 'html' || ext === 'htm') return 'html';
  if (type.startsWith('text/') || TEXTS.some((t) => type.includes(t)) || TEXT_EXT.includes(ext)) return 'text';
  return 'other';
}

// streamed is the address of a file a tag of the page reads itself, with the token, as players do.
export const streamed = (url = '') => url + (url.includes('?') ? '&' : '?') + 'token=' + encodeURIComponent(token());

// fetchText reads the start of a file as text, at most TEXT bytes, and says whether it goes on.
export async function fetchText(url: string): Promise<{ text: string; cut: boolean }> {
  const r = await fetch(url, { headers: { Authorization: 'Bearer ' + token(), Range: `bytes=0-${TEXT - 1}` } });
  if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
  const b = new Uint8Array(await r.arrayBuffer());
  const total = Number(/\/(\d+)$/.exec(r.headers.get('Content-Range') ?? '')?.[1] ?? b.length);
  return { text: new TextDecoder().decode(b.slice(0, TEXT)), cut: total > TEXT || b.length > TEXT };
}
