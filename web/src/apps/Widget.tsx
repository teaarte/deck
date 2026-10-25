import { useMemo } from 'react';
import { Small } from '../blocks/around';
import { ScreenView } from '../screen/ScreenView';
import s from './apps.module.css';

// Widget is a small screen in a card of the tiles: the view of a module for the size of the card, or a screen of
// deck, read and kept fresh as any screen; a press on it opens the card's link, its buttons press as on a screen.
export function Widget({ project, screen, size }: { project?: string; screen: string; size: string }) {
  const route = useMemo(() => ({ project, screen, params: { widget: size } }), [project, screen, size]);
  return (
    <div className={s.widget} data-widget={size}>
      <Small.Provider value>
        <ScreenView route={route} />
      </Small.Provider>
    </div>
  );
}
