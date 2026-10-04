import { AreaChart, BarChart, LineChart } from '@mantine/charts';
import '@mantine/charts/styles.css';
import { useLook } from '../../theme/look';
import { formatted, show, when, type Shown } from '../../lib/format';
import type { ChartValue } from '../../lib/catalog.gen';

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
    if (s.format === 'percent') return new Intl.NumberFormat(undefined, { style: 'percent', maximumFractionDigits: 1 }).format(v);
    if (s.format === 'money' && s.currency) {
      return new Intl.NumberFormat(undefined, { style: 'currency', currency: s.currency, notation: 'compact', maximumFractionDigits: 1 }).format(v);
    }
    if (s.format === 'bytes' || s.format === 'duration') return formatted(v, s);
  } catch {
    // A currency the device does not know falls back to the number.
  }
  return new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(v);
}

// Chart draws the series of a source over x: lines, areas or bars; times on x make a time axis.
export function Chart({ value, kind, s }: { value: ChartValue; kind?: string; s: Shown }) {
  const look = useLook();
  const ms = times(value.x);
  const span = ms && ms.length > 1 ? Math.max(...ms) - Math.min(...ms) : 0;
  // A date without a time is a day of the calendar, not midnight of Greenwich: it is shown in UTC.
  const days = value.x.every((x) => typeof x === 'string' && x.length === 10);
  const zone = days ? { timeZone: 'UTC' } : {};
  const tick = (v: number) => {
    const d = new Date(v);
    return span < 2 * 86400000 && !days ? d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) : d.toLocaleDateString(undefined, zone);
  };
  const data = value.x.map((x, i) => {
    const row: Record<string, unknown> = { x: ms && kind !== 'bar' ? ms[i] : ms ? tick(ms[i]) : show(x) };
    value.series.forEach((sr, j) => { row['s' + j] = sr.values[i] ?? null; });
    return row;
  });
  const colors = [look.accent, ...SERIES.filter((c) => c !== look.accent)];
  const series = value.series.map((sr, j) => ({ name: 's' + j, label: sr.label, color: colors[j % colors.length] + '.6' }));
  const common = {
    h: 260, data, dataKey: 'x', series, withLegend: series.length > 1,
    valueFormatter: (v: number) => formatted(v, s),
    yAxisProps: { domain: kind === 'bar' ? undefined : ['auto', 'auto'], tickFormatter: (v: number) => short(v, s), width: 56 },
    xAxisProps: ms && kind !== 'bar'
      ? { type: 'number' as const, scale: 'time' as const, domain: ['dataMin', 'dataMax'], tickFormatter: tick }
      : undefined,
    tooltipProps: ms && kind !== 'bar'
      ? { labelFormatter: (v: unknown) => (days ? new Date(v as number).toLocaleDateString(undefined, zone) : new Date(v as number).toLocaleString()) }
      : undefined,
  };
  if (kind === 'bar') return <BarChart {...common} data-chart="bar" />;
  if (kind === 'area') return <AreaChart {...common} data-chart="area" connectNulls={false} withDots={false} />;
  return <LineChart {...common} data-chart="line" connectNulls={false} withDots={data.length <= 60} />;
}
