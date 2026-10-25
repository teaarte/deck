import { Catalog } from '@a2ui/web_core/v0_9';
import { Button, Text, type ReactComponentImplementation } from '@a2ui/react/v0_9';
import catalogSchema from '../lib/catalogSchema';
import { actions } from '../blocks/actions';
import { appearance } from '../blocks/appearance';
import { card } from '../blocks/card';
import { chart } from '../blocks/chart';
import { chat } from '../blocks/chat';
import { custom } from '../blocks/custom';
import { documentBlock } from '../blocks/document';
import { filter } from '../blocks/filter';
import { form } from '../blocks/form';
import { list } from '../blocks/list';
import { log } from '../blocks/log';
import { media } from '../blocks/media';
import { modal } from '../blocks/modal';
import { column, row } from '../blocks/row';
import { screen } from '../blocks/screen';
import { stat } from '../blocks/stat';
import { table } from '../blocks/table';
import { tabs } from '../blocks/tabs';
import { unknown } from '../blocks/unknown';

export const CATALOG_ID = catalogSchema.catalogId;
export { UNKNOWN } from '../blocks/unknown';
export type { Action } from '../lib/catalog.gen';

// The standard components are A2UI's; deck draws its containers in its own look, a button and a text as A2UI does.
const impls: ReactComponentImplementation[] = [
  row, column, tabs, modal, Button, Text,
  screen, table, list, card, stat, chart, media, actions, filter, log, form, custom, documentBlock, chat, appearance, unknown,
];

export const catalog = new Catalog<ReactComponentImplementation>(CATALOG_ID, impls);
export const known = new Set(impls.map((c) => c.name));
