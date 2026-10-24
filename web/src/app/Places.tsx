import { useTranslation } from 'react-i18next';
import { placesOf, useMenuData } from '../hooks/useMenu';
import { NamedIcon } from '../ui/NamedIcon';
import { href, useRoute } from './router';
import s from './shell.module.css';

// Places are the tabs of the screens of the project or of deck the owner is in, when the cards stand for the menu.
export function Places() {
  const { t } = useTranslation();
  const route = useRoute();
  const places = placesOf(useMenuData(), route.project);
  if (places.length < 2) return null;
  return (
    <nav className={s.places} aria-label={t('nav.screens')} data-places>
      {places.map((p) => (
        <a key={p.name} href={href({ project: p.project, screen: p.name })} className={s.place}
          aria-current={p.name === route.screen ? 'page' : undefined}>
          <NamedIcon name={p.icon} fallback={p.title} size={16} />
          <span>{p.title}</span>
        </a>
      ))}
    </nav>
  );
}
