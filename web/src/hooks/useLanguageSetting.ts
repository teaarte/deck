import { useEffect } from 'react';
import { call } from '../lib/api';
import { events } from '../lib/events';
import { setLanguage } from '../lib/i18n';

// useLanguageSetting follows the language the workspace picked; a change draws deck again in it.
export function useLanguageSetting() {
  useEffect(() => {
    const load = () => {
      call<{ name: string; value: unknown }[]>('deck.settings_list').then((ss) => {
        const l = ss.find((x) => x.name === 'language');
        if (l) setLanguage(String(l.value));
      }, () => undefined);
    };
    load();
    return events((e) => { if (e.topic === 'settings') load(); }, load);
  }, []);
}
