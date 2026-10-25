import { kindOf, PREVIEW, streamed, type Item } from './kinds';
import { TypeIcon } from './TypeIcon';
import s from './files.module.css';

// Thumb is a small picture of a file: the light copy of a picture, else the picture itself, the first frame of a video,
// else the badge of its type.
export function Thumb({ it, size }: { it: Item; size: number }) {
  const kind = kindOf(it);
  const box = { width: size, height: size };
  if (!it.error && kind === 'image' && (it.thumb || (it.size ?? 0) <= PREVIEW)) {
    return <img className={s.thumb} style={{ ...box, background: it.color }} src={streamed(it.thumb ?? it.url)} alt="" loading="lazy" decoding="async" data-thumb="image" />;
  }
  if (!it.error && kind === 'video') {
    return <video className={s.thumb} style={box} src={streamed(it.url) + '#t=0.1'} preload="metadata" muted playsInline data-thumb="video" />;
  }
  return <span className={s.icon} style={box}><TypeIcon it={it} size={Math.round(size * 0.6)} /></span>;
}
