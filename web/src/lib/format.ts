
// Shown is how the catalog says to show a value: its format and the currency of money.
export interface Shown {
  format?: string;
  currency?: string;
}

const MOMENT = /^\d{4}-\d\d-\d\dT\d\d:\d\d(:\d\d(\.\d+)?)?(Z|[+-]\d\d:\d\d)$/;

// text is a value as plain text: an object as JSON, nothing as empty.
export const text = (v: unknown): string => (v === null || v === undefined ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v));

// show is a value as the owner reads it: a moment in the time of the device, the rest as text.
export const show = (v: unknown): string => (typeof v === 'string' && MOMENT.test(v) ? new Date(v).toLocaleString() : text(v));

const ISO = /^\d{4}-\d\d-\d\d([T ]\d\d:\d\d(:\d\d(\.\d+)?)?(Z|[+-]\d\d:?\d\d)?)?$/;

// when is the time of an ISO 8601 text with a date the calendar has; anything else is no time.
export const when = (v: unknown): Date | undefined => {
  if (typeof v !== 'string' || !ISO.test(v)) return undefined;
  const d = new Date(v);
  if (isNaN(d.getTime())) return undefined;
  const [y, m, day] = v.slice(0, 10).split('-').map(Number);
  const real = new Date(Date.UTC(y, m - 1, day));
  return real.getUTCMonth() === m - 1 && real.getUTCDate() === day ? d : undefined;
};

const STEPS: [Intl.RelativeTimeFormatUnit, number][] = [['second', 60], ['minute', 60], ['hour', 24], ['day', 7], ['week', 4.35], ['month', 12], ['year', Infinity]];

export function relative(d: Date, now: number): string {
  let n = (d.getTime() - now) / 1000;
  for (const [unit, size] of STEPS) {
    if (Math.abs(n) < size) return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(Math.round(n), unit);
    n /= size;
  }
  return d.toLocaleString();
}

function duration(s: number): string {
  if (s < 0) return '−' + duration(-s);
  s = Math.round(s);
  const parts: [number, string][] = [[Math.floor(s / 86400), 'day'], [Math.floor(s / 3600) % 24, 'hour'], [Math.floor(s / 60) % 60, 'minute'], [s % 60, 'second']];
  const kept = parts.filter(([n]) => n > 0).slice(0, 2);
  if (!kept.length) kept.push([0, 'second']);
  return kept.map(([n, unit]) => new Intl.NumberFormat(undefined, { style: 'unit', unit, unitDisplay: 'short' }).format(n)).join(' ');
}

// timecode is a time in seconds as a player shows it: h:mm:ss or m:ss, with hundredths when it has a fraction.
export function timecode(s: number): string {
  if (s < 0) return '−' + timecode(-s);
  const cs = Math.round(s * 100);
  const whole = Math.floor(cs / 100);
  const [h, m, sec] = [Math.floor(whole / 3600), Math.floor(whole / 60) % 60, whole % 60];
  const pad = (n: number) => String(n).padStart(2, '0');
  const text = h ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
  return cs % 100 ? `${text}.${pad(cs % 100)}` : text;
}

const BYTES = ['byte', 'kilobyte', 'megabyte', 'gigabyte', 'terabyte', 'petabyte'];

function bytes(n: number): string {
  let i = 0;
  while (Math.abs(n) >= 1000 && i < BYTES.length - 1) {
    n /= 1000;
    i++;
  }
  return new Intl.NumberFormat(undefined, { style: 'unit', unit: BYTES[i], unitDisplay: 'short', maximumFractionDigits: i ? 1 : 0 }).format(n);
}

// formatted is a value as text by its format, in the locale and time zone of the device; a value of another
// type stays as it is.
export function formatted(v: unknown, s: Shown, now = Date.now()): string {
  const n = typeof v === 'number' ? v : undefined;
  try {
    switch (s.format) {
      case 'number':
        if (n !== undefined) return new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(n);
        break;
      case 'compact':
        if (n !== undefined) return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(n);
        break;
      case 'percent':
        if (n !== undefined) return new Intl.NumberFormat(undefined, { style: 'percent', maximumFractionDigits: 1 }).format(n);
        break;
      case 'money':
        if (n !== undefined && s.currency) return new Intl.NumberFormat(undefined, { style: 'currency', currency: s.currency }).format(n);
        break;
      case 'duration':
        if (n !== undefined) return duration(n);
        break;
      case 'bytes':
        if (n !== undefined) return bytes(n);
        break;
      case 'timecode':
        if (n !== undefined) return timecode(n);
        break;
      case 'date': {
        const d = when(v);
        if (d) return /^\d{4}-\d\d-\d\d$/.test(v as string) ? d.toLocaleDateString(undefined, { timeZone: 'UTC' }) : d.toLocaleDateString();
        break;
      }
      case 'datetime': {
        const d = when(v);
        if (d) return d.toLocaleString();
        break;
      }
      case 'relative': {
        const d = when(v);
        if (d) return relative(d, now);
        break;
      }
    }
  } catch {
    // A currency the device does not know shows the value as it is.
  }
  return show(v);
}

// TONES are the colors of the tones a value takes.
const TONES: Record<string, string> = { ok: 'green', warn: 'orange', error: 'red', muted: 'dimmed' };
export const toneColor = (tone?: string | null) => (tone ? TONES[tone] : undefined);
