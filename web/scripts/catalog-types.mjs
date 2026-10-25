// Writes src/lib/catalog.gen.ts: the types of block data and of block props from schemas/catalog.json.
import { compile } from 'json-schema-to-typescript';
import { readFileSync, writeFileSync } from 'node:fs';

const { $defs, components } = JSON.parse(readFileSync(new URL('../../schemas/catalog.json', import.meta.url), 'utf8'));
const { values, ...defs } = $defs;
delete values.description;
for (const [name, v] of Object.entries(values)) defs[name[0].toUpperCase() + name.slice(1) + 'Value'] = v;
// A prop as a block draws it: children are ids, the data and the other data a block reads are the value at their path.
const drawn = (p, key) => {
  if (key === 'data' || p.$ref?.endsWith('/DynamicValue') && p.description?.startsWith('value:')) return { $ref: '#/$defs/data' };
  if (p.$ref?.endsWith('/ChildList')) return { type: 'array', items: { type: 'string' }, description: p.description };
  return p;
};
for (const [name, c] of Object.entries(components)) {
  const properties = Object.fromEntries(Object.entries(c.properties).map(([k, p]) => [k, drawn(p, k)]));
  defs[name[0].toUpperCase() + name.slice(1) + 'Props'] = { type: 'object', additionalProperties: false, required: (c.required ?? []).filter((k) => k !== 'data'), properties };
}
const refs = Object.fromEntries(Object.keys(defs).map((k) => [k, { $ref: '#/$defs/' + k }]));
const ts = await compile({ title: 'Catalog', type: 'object', $defs: defs, properties: refs }, 'Catalog',
  { bannerComment: '// Code generated from schemas/catalog.json by scripts/catalog-types.mjs. DO NOT EDIT.', additionalProperties: false, unreachableDefinitions: true });
writeFileSync(new URL('../src/lib/catalog.gen.ts', import.meta.url), ts.replace(/export type Action1 = Action;\n/g, ''));
