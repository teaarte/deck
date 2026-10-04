import { upload } from '../../lib/api';
import i18n from '../../lib/i18n';

export interface Schema {
  type?: string | string[];
  format?: string;
  enum?: unknown[];
  items?: Schema;
  properties?: Record<string, Schema>;
  required?: string[];
  description?: string;
  title?: string;
  writeOnly?: boolean;
}

export const kind = (s: Schema) => (Array.isArray(s.type) ? s.type.find((x) => x !== 'null') : s.type) ?? '';
export const isFile = (s: Schema) => s.format === 'file' || s.items?.format === 'file';

// toLocal is an ISO time as a datetime-local field shows it; fromLocal is the way back.
export const toLocal = (iso: string) => {
  const d = new Date(iso);
  return isNaN(+d) ? iso : new Date(+d - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
};
export const fromLocal = (v: string) => (v ? new Date(v).toISOString() : '');

// buildInput builds the call of a form; an edit starts from the record, so fields the form does not show stay as
// they were. Files go up first and are passed by id.
export async function buildInput(props: Record<string, Schema>, values: Record<string, unknown>, files: Record<string, File[]>,
  { project, record }: { project: string; record?: Record<string, unknown> }) {
  const data: Record<string, unknown> = record ? Object.fromEntries(Object.entries(record).filter(([k]) => !k.startsWith('_'))) : {};
  for (const [name, s] of Object.entries(props)) {
    if (isFile(s)) {
      const fs = files[name] ?? [];
      if (!fs.length) continue;
      const ids = (await upload(project, fs)).map((f) => String(f.id));
      data[name] = kind(s) === 'array' ? ids : ids[0];
      continue;
    }
    let v = values[name];
    if (v === undefined || v === '') {
      delete data[name];
      continue;
    }
    if (!['string', 'integer', 'number', 'boolean', 'array'].includes(kind(s)) && typeof v === 'string' && !s.enum) {
      try {
        v = JSON.parse(v);
      } catch {
        throw new Error(i18n.t('form.json', { field: name }));
      }
    }
    data[name] = v;
  }
  return data;
}
