import { useId, useRef } from 'react';
import { Group, Input, NumberInput, Slider } from '@mantine/core';
import { useElementSize } from '@mantine/hooks';
import { locale } from '../../lib/i18n';
import type { Schema } from './schema';
import s from './Range.module.css';

// MARKS is how many marks a slider may name; past that they stand as dots.
const MARKS = 6;
// PULL is the part of the track near a mark within which a dragged thumb takes the mark.
const PULL = 0.03;
// CHAR is about the width of a character of the name of a mark, in pixels, and GAP the least room between two names.
const CHAR = 7;
const GAP = 10;

// Span is where a slider goes: its ends on the step grid inside the bounds, its step, and the digits its values keep.
export type Span = { lo: number; hi: number; step: number; digits: number; grid: boolean };

const digitsOf = (n: number) => Math.min(6, Math.max(0, -Math.floor(Math.log10(n) + 1e-9)));
const round = (n: number, digits: number) => Number(n.toFixed(digits));

// span is the slider of a number by its schema: integer steps by 1, multipleOf by itself from zero as deck checks it,
// another number by a round hundredth of its range; none when the bounds leave no room.
export function span(schema: Schema, integer: boolean): Span | undefined {
  const lower = schema.minimum ?? schema.exclusiveMinimum;
  const upper = schema.maximum ?? schema.exclusiveMaximum;
  if (typeof lower !== 'number' || typeof upper !== 'number' || upper <= lower) return undefined;
  const grid = !!schema.multipleOf || integer;
  const step = schema.multipleOf ?? (integer ? 1 : 10 ** Math.floor(Math.log10((upper - lower) / 100)));
  const digits = digitsOf(step);
  let lo = grid ? round(Math.ceil(round(lower / step, 9)) * step, digits) : lower;
  let hi = grid ? round(Math.floor(round(upper / step, 9)) * step, digits) : upper;
  if (schema.minimum === undefined && lo <= lower) lo = round(lo + step, digits);
  if (schema.maximum === undefined && hi >= upper) hi = round(hi - step, digits);
  return hi > lo ? { lo, hi, step, digits, grid } : undefined;
}

// Range is a number between the bounds of its schema: the exact value beside its label, a slider under it whose thumb
// takes the marks of examples when dragged near them, and stays between them too.
export function Range({ name, schema, label, value, readOnly, required, at, onValue }: {
  name: string; schema: Schema; label: string; value: unknown; readOnly: boolean; required: boolean; at: Span;
  onValue: (v: number | undefined) => void;
}) {
  const id = useId();
  const { ref, width } = useElementSize();
  const { lo, hi, step, digits, grid } = at;
  const onGrid = (x: number) => !grid || Math.abs(round(x / step, 9) - Math.round(x / step)) < 1e-6;
  const marks = [...new Set(Array.isArray(schema.examples) ? schema.examples : [])]
    .filter((x): x is number => typeof x === 'number' && x >= lo && x <= hi && onGrid(x)).sort((a, b) => a - b);
  const fmt = new Intl.NumberFormat(locale(), { maximumFractionDigits: digits });
  // A few marks are named, from the last back, each with room for its name beside the one named after it.
  const names = new Set<number>();
  if (marks.length <= MARKS && width > 0) {
    let after: number | undefined;
    for (const m of [...marks].reverse()) {
      const room = after === undefined ? Infinity : ((after - m) / (hi - lo)) * width;
      if (after === undefined || room >= ((fmt.format(m).length + fmt.format(after).length) / 2) * CHAR + GAP) {
        names.add(m);
        after = m;
      }
    }
  }
  const now = typeof value === 'number' ? value : undefined;
  // Only a finger or a mouse is pulled to a mark; the arrows step past it.
  const dragging = useRef(false);
  const grab = () => {
    dragging.current = true;
    const drop = () => { dragging.current = false; window.removeEventListener('pointerup', drop); window.removeEventListener('pointercancel', drop); };
    window.addEventListener('pointerup', drop);
    window.addEventListener('pointercancel', drop);
  };
  const pull = (v: number) => {
    if (!dragging.current) return v;
    const near = marks.reduce<number | undefined>((best, m) => (best === undefined || Math.abs(m - v) < Math.abs(best - v) ? m : best), undefined);
    return near !== undefined && Math.abs(near - v) <= PULL * (hi - lo) ? near : v;
  };
  return (
    <div ref={ref} className={s.range} data-field={name} data-range>
      <Group justify="space-between" wrap="nowrap" gap="sm" align="center">
        <Input.Label required={required} htmlFor={id}>{label}</Input.Label>
        <NumberInput id={id} className={s.exact} size="sm" min={lo} max={hi} step={step} allowDecimal={digits > 0} decimalScale={digits}
          clampBehavior="blur" disabled={readOnly} value={now ?? ''} data-range-value
          onChange={(v) => onValue(typeof v === 'number' ? v : v === '' ? undefined : Number(v))} />
      </Group>
      {schema.description && <Input.Description>{schema.description}</Input.Description>}
      <Slider className={s.slider} data-named={names.size > 0 || undefined} data-empty={now === undefined || undefined}
        min={lo} max={hi} step={step} precision={digits} disabled={readOnly} value={now ?? lo} thumbSize={22} size="md"
        label={(v) => fmt.format(v)} marks={marks.map((m) => ({ value: m, label: names.has(m) ? fmt.format(m) : undefined }))}
        thumbLabel={label} onChange={(v) => onValue(pull(v))} onPointerDown={grab} data-range-slider />
    </div>
  );
}
