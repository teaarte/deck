import '@mantine/core/styles.css';
import '../src/lib/i18n';
import { createRoot } from 'react-dom/client';
import { MantineProvider } from '@mantine/core';
import { A2uiSurface, basicCatalog } from '@a2ui/react/v0_9';
import { Catalog, MessageProcessor, type A2uiClientAction } from '@a2ui/web_core/v0_9';
import catalogSchema from '../src/lib/catalogSchema';
import { token } from '../src/lib/api';
import { actions } from '../src/blocks/actions';
import { appearance } from '../src/blocks/appearance';
import { card } from '../src/blocks/card';
import { chart } from '../src/blocks/chart';
import { chat } from '../src/blocks/chat';
import { custom } from '../src/blocks/custom';
import { documentBlock } from '../src/blocks/document';
import { form } from '../src/blocks/form';
import { list } from '../src/blocks/list';
import { log } from '../src/blocks/log';
import { media } from '../src/blocks/media';
import { screen } from '../src/blocks/screen';
import { stat } from '../src/blocks/stat';
import { table } from '../src/blocks/table';

// The stock components of A2UI for the basic part of the catalog, deck's blocks for the rest; nothing of deck's host.
const basic = catalogSchema.basic.components.map((name: string) => basicCatalog.components.get(name)!);
const catalog = new Catalog(catalogSchema.catalogId, [...basic, screen, table, list, card, stat, chart, media, actions, log, form, appearance, documentBlock, custom, chat]);

const q = new URLSearchParams(location.search);
const heard: A2uiClientAction[] = [];
const shown = document.createElement('pre');
shown.setAttribute('data-foreign-actions', '');
document.body.append(shown);
const input = { screen: q.get('screen'), ...(q.get('project') ? { project: q.get('project') } : {}) };
const screenOf = async (extra: Record<string, unknown> = {}) => (await fetch('/api/commands/deck.screen', {
  method: 'POST', headers: { Authorization: 'Bearer ' + token(), 'Content-Type': 'application/json' }, body: JSON.stringify({ ...input, ...extra }),
})).json();
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
