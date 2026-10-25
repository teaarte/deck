import { useTranslation } from 'react-i18next';
import { placesOf, useMenuData } from '../hooks/useMenu';
import { NamedIcon } from '../ui/NamedIcon';
import { href, useRoute } from './router';
import s from './shell.module.css';

// Places are the tabs of the screens of the project or of deck the owner is in, when the cards stand for the menu.
// slot is where a screen puts the button of its side panel on a phone, at the end of the row.
export function Places({ slot }: { slot?: (el: HTMLElement | null) => void }) {
  const { t } = useTranslation();
  const route = useRoute();
  const places = placesOf(useMenuData(), route.project);
  if (places.length < 2) return <div ref={slot} className={s.slot} data-side-slot data-alone />;
  return (
    <div className={s.placesRow}>
    <nav className={s.places} aria-label={t('nav.screens')} data-places>
      {places.map((p) => (
        <a key={p.name} href={href({ project: p.project, screen: p.name })} className={s.place}
          aria-current={p.name === route.screen ? 'page' : undefined} data-active={p.name === route.screen || undefined}>
          <NamedIcon name={p.icon} fallback={p.title} size={16} />
          <span>{p.title}</span>
        </a>
      ))}
    </nav>
    <div ref={slot} className={s.slot} data-side-slot />
    </div>
  );
}
