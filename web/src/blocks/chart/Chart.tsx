import { BarChart, LineChart } from '@mantine/charts';
import { Paper, Text } from '@mantine/core';
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
      // Cents stay on an axis of small sums, which would all read $0 rounded.
      const small = Math.abs(v) > 0 && Math.abs(v) < 10;
      return new Intl.NumberFormat(locale(), { style: 'currency', currency: s.currency, notation: 'compact', maximumFractionDigits: small ? 2 : 1 }).format(v);
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
  // The ticks may wear a larger font of the theme than 12px: their room is measured at 14px, so none is cut.
  ruler.font = `14px ${getComputedStyle(document.body).fontFamily}`;
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
    const row: Record<string, unknown> = { x: ms && kind !== 'bar' ? ms[i] : ms ? tick(ms[i]) : show(x), details: value.details?.[i] };
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
    tooltipProps: {
      ...(ms && kind !== 'bar'
        ? { labelFormatter: (v: unknown) => (days ? new Date(v as number).toLocaleDateString(locale(), zone) : new Date(v as number).toLocaleString(locale())) }
        : {}),
      // A point with details says them under its values, such as what each model spent that day.
      ...(value.details ? { content: ({ label, payload }: { label?: unknown; payload?: readonly { payload?: Record<string, unknown> }[] }) => (
        <Details label={label} payload={payload} series={series} format={(v: number) => formatted(v, s)}
          title={ms && kind !== 'bar' && typeof label === 'number' ? (days ? new Date(label).toLocaleDateString(locale(), zone) : new Date(label).toLocaleString(locale())) : show(label)} />
      ) } : {}),
    },
  };
  if (kind === 'bar') return <BarChart {...common} data-chart="bar" />;
  return <LineChart {...common} data-chart="line" connectNulls={false} withDots={data.length <= 60} />;
}

// Details is the tooltip of a point of a chart with details: its time, the value of each series and its details.
function Details({ payload, series, format, title }: { label?: unknown; payload?: readonly { payload?: Record<string, unknown> }[]; series: { name: string; label?: string }[];
  format: (v: number) => string; title: string }) {
  const row = payload?.[0]?.payload;
  if (!row) return null;
  return (
    <Paper p="xs" shadow="md" withBorder data-chart-details>
      <Text size="xs" fw={700}>{title}</Text>
      {series.map((sr) => typeof row[sr.name] === 'number' && <Text key={sr.name} size="xs">{sr.label}: {format(row[sr.name] as number)}</Text>)}
      {typeof row.details === 'string' && row.details && <Text size="xs" c="dimmed" className="deck-pre" mt={4}>{row.details}</Text>}
    </Paper>
  );
}
