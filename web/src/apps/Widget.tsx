import { useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Fit, Small, thirds } from '../blocks/around';
import catalogSchema from '../lib/catalogSchema';
import { ScreenView } from '../screen/ScreenView';
import s from './apps.module.css';

// lines are the thirds of a line each part of a widget takes, by the catalog the server lays it by.
const lines = { '--title-thirds': catalogSchema.widget.title, '--line-thirds': catalogSchema.widget.line, '--stat-thirds': catalogSchema.widget.stat,
  '--chart-thirds': catalogSchema.widget.chart };

// Widget is a small screen in a card of the tiles, its blocks in the thirds of a line the server gave them; a press
// opens the card's link, its buttons press as on a screen; rest stands in the room they leave.
export function Widget({ project, screen, size, rest }: { project?: string; screen: string; size: string; rest?: ReactNode }) {
  const { t } = useTranslation();
  const route = useMemo(() => ({ project, screen, params: { widget: size } }), [project, screen, size]);
  const h = Number(size.split('x')[1]) || 1;
  // Only a new size comes in softly: a card drawn again, as on a way back to the tiles, stands as it was.
  const was = useRef({ size, resized: false });
  if (was.current.size !== size) was.current = { size, resized: true };
  const out = useLeftOut();
  return (
    <div ref={out.ref} className={s.widget} data-widget={size} data-resized={was.current.resized || undefined} data-fills={!rest || undefined}
      style={{ '--thirds': thirds(h), ...lines } as CSSProperties}>
      <Small.Provider value>
        <Fit.Provider value>
          <ScreenView route={route} />
        </Fit.Provider>
      </Small.Provider>
      {rest}
      {out.names.length > 0 && (
        <span className={s.leftOut} title={t('Not in the card: {{names}}', { names: out.names.join(', ') })} aria-label={t('Not in the card: {{names}}', { names: out.names.join(', ') })}
          data-widget-left-out={out.names.length}>+{out.names.length}</span>
      )}
    </div>
  );
}

// useLeftOut are the titles of the blocks of a widget the card has no room for on this device: they wrap out of sight.
function useLeftOut() {
  const { t } = useTranslation();
  const ref = useRef<HTMLDivElement>(null);
  const [names, setNames] = useState<string[]>([]);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const look = () => {
      const grid = el.querySelector<HTMLElement>('[data-grid][data-fit]');
      const cells = grid ? [...grid.children] as HTMLElement[] : [];
      const right = grid?.getBoundingClientRect().right ?? 0;
      const left = cells.filter((c) => c.getBoundingClientRect().left >= right - 1);
      // A block out of sight takes no focus either.
      cells.forEach((c) => { c.inert = left.includes(c); });
      const out = left.map((c) => {
        const b = c.querySelector<HTMLElement>('[data-block]');
        return b?.getAttribute('aria-label') || b?.dataset.title || t(c.querySelector('[data-chart-block]') ? 'a chart' : 'a block');
      });
      setNames((was) => (was.join('\n') === out.join('\n') ? was : out));
    };
    const seen = new ResizeObserver(look);
    seen.observe(el);
    const changed = new MutationObserver(look);
    changed.observe(el, { childList: true, subtree: true });
    look();
    return () => { seen.disconnect(); changed.disconnect(); };
  }, [t]);
  return { ref, names };
}
