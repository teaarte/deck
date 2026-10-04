import { Catalog } from '@a2ui/web_core/v0_9';
import type { ReactComponentImplementation } from '@a2ui/react/v0_9';
import catalogSchema from './lib/catalogSchema';
import { actions } from './blocks/actions';
import { appearance } from './blocks/appearance';
import { board } from './blocks/board';
import { card } from './blocks/card';
import { chart } from './blocks/chart';
import { chat } from './blocks/chat';
import { columns } from './blocks/columns';
import { custom } from './blocks/custom';
import { dialog } from './blocks/dialog';
import { documentBlock } from './blocks/document';
import { form } from './blocks/form';
import { list } from './blocks/list';
import { log } from './blocks/log';
import { media } from './blocks/media';
import { screen } from './blocks/screen';
import { stat } from './blocks/stat';
import { table } from './blocks/table';
import { tabs } from './blocks/tabs';
import { unknown } from './blocks/unknown';

export const CATALOG_ID = catalogSchema.catalogId;
export { UNKNOWN } from './blocks/unknown';
export type { Action } from './lib/catalog.gen';

const impls: ReactComponentImplementation[] = [
  screen, columns, tabs, dialog, table, list, card, stat, chart, media, actions, log, board, form, custom, documentBlock, chat, appearance, unknown,
];

export const catalog = new Catalog<ReactComponentImplementation>(CATALOG_ID, impls);
export const known = new Set(impls.map((c) => c.name));
