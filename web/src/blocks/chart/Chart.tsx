import { BarChart, LineChart } from '@mantine/charts';
import '@mantine/charts/styles.css';
import { useLook } from '../../theme/look';
import { formatted, show, when, type Shown } from '../../lib/format';
import type { ChartValue } from '../../lib/catalog.gen';
import { locale } from '../../lib/i18n';

// SERIES are the colors of the series after the first, which takes the accent of the device.
const SERIES = ['orange', 'teal', 'grape', 'yellow', 'cyan', 'pink', 'lime', 'indigo'];

// times are the x values as milliseconds when every one of them is an ISO 8601 time.
function times(xs: unknown[]): number[] | undefined {
  const ds = xs.map(when);
  return xs.length && ds.every(Boolean) ? ds.map((d) => d!.getTime()) : undefined;
}

// short is a value on the y axis: compact, as $80K or 42%.
function short(v: number, s: Shown): string {
  try {
    if (s.format === 'percent') return new Intl.NumberFormat(locale(), { style: 'percent', maximumFractionDigits: 1 }).format(v);
    if (s.format === 'money' && s.currency) {
      return new Intl.NumberFormat(locale(), { style: 'currency', currency: s.currency, notation: 'compact', maximumFractionDigits: 1 }).format(v);
    }
    if (s.format === 'bytes' || s.format === 'duration') return formatted(v, s);
  } catch {
    // A currency the device does not know falls back to the number.
  }
  return new Intl.NumberFormat(locale(), { notation: 'compact', maximumFractionDigits: 1 }).format(v);
}

const DAY = 86400000;

// days are the starts of the days from a to b, or of the months over a long span, as ticks that each name one date.
function dayTicks(a: number, b: number, utc: boolean): number[] {
  const months = b - a > 120 * DAY;
  const d = new Date(a);
  if (utc) d.setUTCHours(0, 0, 0, 0);
  else d.setHours(0, 0, 0, 0);
  if (months) {
    if (utc) d.setUTCDate(1);
    else d.setDate(1);
  }
  const out: number[] = [];
  while (d.getTime() <= b) {
    if (d.getTime() >= a) out.push(d.getTime());
    if (months) {
      if (utc) d.setUTCMonth(d.getUTCMonth() + 1);
      else d.setMonth(d.getMonth() + 1);
    } else if (utc) d.setUTCDate(d.getUTCDate() + 1);
    else d.setDate(d.getDate() + 1);
  }
  return out;
}

let ruler: CanvasRenderingContext2D | null | undefined;

// wide is how wide the labels of the y axis are in pixels, by the longest of those of the ends and the middle.
function wide(values: number[], s: Shown): number {
  if (!values.length) return 40;
  ruler ??= document.createElement('canvas').getContext('2d');
  if (!ruler) return 64;
  ruler.font = `12px ${getComputedStyle(document.body).fontFamily}`;
  const lo = Math.min(...values), hi = Math.max(...values);
  const span = hi - lo || Math.abs(hi) || 1;
  const labels = [lo - span * 0.1, lo, (lo + hi) / 2, hi, hi + span * 0.1].map((v) => short(v, s));
  return Math.ceil(Math.max(...labels.map((l) => ruler!.measureText(l).width))) + 12;
}

// Chart draws the series of a source over x: lines or bars; times on x make a time axis.
export function Chart({ value, kind, s, h = 260 }: { value: ChartValue; kind?: string; s: Shown; h?: number | string }) {
  const look = useLook();
  const ms = times(value.x);
  const span = ms && ms.length > 1 ? Math.max(...ms) - Math.min(...ms) : 0;
  // A date without a time is a day of the calendar, not midnight of Greenwich: it is shown in UTC.
  const days = value.x.every((x) => typeof x === 'string' && x.length === 10);
  const zone = days ? { timeZone: 'UTC' } : {};
  const tick = (v: number) => {
    const d = new Date(v);
    return span < 2 * 86400000 && !days ? d.toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' }) : d.toLocaleDateString(locale(), zone);
  };
  const data = value.x.map((x, i) => {
    const row: Record<string, unknown> = { x: ms && kind !== 'bar' ? ms[i] : ms ? tick(ms[i]) : show(x) };
    value.series.forEach((sr, j) => { row['s' + j] = sr.values[i] ?? null; });
    return row;
  });
  const colors = [look.accent, ...SERIES.filter((c) => c !== look.accent)];
  const series = value.series.map((sr, j) => ({ name: 's' + j, label: sr.label, color: colors[j % colors.length] + '.6' }));
  const values = value.series.flatMap((sr) => sr.values).filter((v): v is number => typeof v === 'number');
  // Dates on the axis go one a day at the most, so no two ticks say the same date.
  const ticks = ms && kind !== 'bar' && (days || span >= 2 * DAY) ? dayTicks(Math.min(...ms), Math.max(...ms), days) : undefined;
  const common = {
    h, data, dataKey: 'x', series, withLegend: series.length > 1,
    valueFormatter: (v: number) => formatted(v, s),
    yAxisProps: { domain: kind === 'bar' ? undefined : ['auto', 'auto'], tickFormatter: (v: number) => short(v, s), width: wide(values, s) },
    xAxisProps: ms && kind !== 'bar'
      ? { type: 'number' as const, scale: 'time' as const, domain: ['dataMin', 'dataMax'], tickFormatter: tick, ticks, minTickGap: 16 }
      : undefined,
    tooltipProps: ms && kind !== 'bar'
      ? { labelFormatter: (v: unknown) => (days ? new Date(v as number).toLocaleDateString(locale(), zone) : new Date(v as number).toLocaleString(locale())) }
      : undefined,
  };
  if (kind === 'bar') return <BarChart {...common} data-chart="bar" />;
  return <LineChart {...common} data-chart="line" connectNulls={false} withDots={data.length <= 60} />;
}
