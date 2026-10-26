import { useMemo, type CSSProperties, type ReactNode } from 'react';
import { Fit, Small, thirds } from '../blocks/around';
import { ScreenView } from '../screen/ScreenView';
import s from './apps.module.css';

// Widget is a small screen in a card of the tiles, its blocks in the thirds of a line the server gave them; a press
// opens the card's link, its buttons press as on a screen; rest stands in the room they leave.
export function Widget({ project, screen, size, rest }: { project?: string; screen: string; size: string; rest?: ReactNode }) {
  const route = useMemo(() => ({ project, screen, params: { widget: size } }), [project, screen, size]);
  const h = Number(size.split('x')[1]) || 1;
  return (
    <div className={s.widget} data-widget={size} style={{ '--thirds': thirds(h) } as CSSProperties}>
      <Small.Provider value>
        <Fit.Provider value>
          <ScreenView route={route} />
        </Fit.Provider>
      </Small.Provider>
      {rest}
    </div>
  );
}
