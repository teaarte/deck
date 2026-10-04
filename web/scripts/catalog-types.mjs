// Writes src/lib/catalog.gen.ts: the types of block data from schemas/catalog.json.
import { compile } from 'json-schema-to-typescript';
import { readFileSync, writeFileSync } from 'node:fs';

const { $defs } = JSON.parse(readFileSync(new URL('../../schemas/catalog.json', import.meta.url), 'utf8'));
const { values, ...defs } = $defs;
delete values.description;
for (const [name, v] of Object.entries(values)) defs[name[0].toUpperCase() + name.slice(1) + 'Value'] = v;
const refs = Object.fromEntries(Object.keys(defs).map((k) => [k, { $ref: '#/$defs/' + k }]));
const ts = await compile({ title: 'Catalog', type: 'object', $defs: defs, properties: refs }, 'Catalog',
  { bannerComment: '// Code generated from schemas/catalog.json by scripts/catalog-types.mjs. DO NOT EDIT.', additionalProperties: false, unreachableDefinitions: true });
writeFileSync(new URL('../src/lib/catalog.gen.ts', import.meta.url), ts.replace(/export type Action1 = Action;\n/g, ''));
