import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from '../locales/en.json';
import ru from '../locales/ru.json';

export const LANGS = ['en', 'ru'];

// resolve is the language of the interface: the one the owner picked, or the device's for auto.
export function resolve(setting: string): string {
  if (LANGS.includes(setting)) return setting;
  const device = (navigator.language || 'en').slice(0, 2);
  return LANGS.includes(device) ? device : 'en';
}

const KEY = 'deck-language';

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, ru: { translation: ru } },
  lng: resolve(localStorage.getItem(KEY) ?? 'auto'),
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
});
document.documentElement.lang = i18n.language;

// setLanguage keeps the language the workspace picked; a change puts the words of the client in it at once, and the
// screens and reads that take a language read again in it, the page and the chat staying where they are.
export function setLanguage(setting: string) {
  localStorage.setItem(KEY, setting);
  const lang = resolve(setting);
  if (lang === i18n.language) return;
  void i18n.changeLanguage(lang);
  document.documentElement.lang = lang;
}

// locale is how numbers, times and dates are written: the device's own when it speaks the language of the interface,
// else the language of the interface.
export const locale = () => (navigator.language.startsWith(i18n.language) ? navigator.language : i18n.language);

export default i18n;
