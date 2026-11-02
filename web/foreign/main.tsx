import '@mantine/core/styles.css';
import '../src/lib/i18n';
import { createRoot } from 'react-dom/client';
import { MantineProvider } from '@mantine/core';
import { A2uiSurface, basicCatalog } from '@a2ui/react/v0_9';
import { Catalog, MessageProcessor, type A2uiClientAction } from '@a2ui/web_core/v0_9';
import catalogSchema from '../src/lib/catalogSchema';
import { call } from '../src/lib/api';
import { blocks } from '../src/screen/catalog';

// The stock components of A2UI for the basic part of the catalog, deck's blocks for the rest; nothing of deck's host.
const basic = catalogSchema.basic.components.map((name: string) => basicCatalog.components.get(name)!);
const catalog = new Catalog(catalogSchema.catalogId, [...basic, ...blocks]);

const q = new URLSearchParams(location.search);
const heard: A2uiClientAction[] = [];
const shown = document.createElement('pre');
shown.setAttribute('data-foreign-actions', '');
document.body.append(shown);
const input = { screen: q.get('screen'), ...(q.get('project') ? { project: q.get('project') } : {}) };
const screenOf = (extra: Record<string, unknown> = {}) => call<any[]>('deck.screen_get', { ...input, ...extra });
// The page hears every action and answers only the read of an opened dialog or a tab shown, with the messages deck gives
// as they are.
const proc = new MessageProcessor([catalog], async (a: A2uiClientAction) => {
  heard.push(a);
  shown.textContent = JSON.stringify(heard);
  if (a.name === 'dialog') proc.processMessages(await screenOf({ block: a.context.block, dialog: a.context.dialog }));
  if (a.name === 'tab') proc.processMessages(await screenOf({ block: a.context.block, [String(a.context.param)]: a.context.key }));
});
try {
  proc.processMessages(await screenOf());
  const surface = [...proc.model.surfacesMap.values()][0];
  createRoot(document.getElementById('root')!).render(<MantineProvider><div data-foreign><A2uiSurface surface={surface} /></div></MantineProvider>);
} catch (e) {
  document.body.setAttribute('data-foreign-error', String(e));
}
