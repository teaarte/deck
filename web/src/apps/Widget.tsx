import { useMemo, type CSSProperties, type ReactNode } from 'react';
import { Fit, Small, thirds } from '../blocks/around';
import { ScreenView } from '../screen/ScreenView';
import s from './apps.module.css';

// Widget is a small screen in a card of the tiles: the view of a module for the size of the card, or a screen of
// deck, read and kept fresh as any screen; a press on it opens the card's link, its buttons press as on a screen.
// Its blocks take the thirds of a line the server gave them; rest stands in the room they leave.
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
