import { useEffect, useState, type RefObject } from 'react';
import { sharedFiles } from '../../lib/share';
import { isFile, type Schema } from './schema';

// useFormValues holds what the owner typed over the values the server sends: a field the owner changed stays theirs
// until the server holds the same, and a form for files takes the files shared to deck.
export function useFormValues(server: Record<string, unknown> | undefined, record: unknown, cmd: { name: string; input?: Schema } | undefined, alive: RefObject<boolean>) {
  const [edited, setEdited] = useState<Record<string, unknown>>({});
  const [files, setFiles] = useState<Record<string, File[]>>({});
  useEffect(() => { setEdited({}); }, [record]);
  useEffect(() => {
    setEdited((e) => {
      const kept = Object.entries(e).filter(([k, v]) => String(v) !== String(server?.[k]));
      return kept.length === Object.keys(e).length ? e : Object.fromEntries(kept);
    });
  }, [server]);
  useEffect(() => {
    const field = cmd && Object.entries(cmd.input?.properties ?? {}).find(([, s]) => isFile(s))?.[0];
    if (!field) return;
    void sharedFiles().then((got) => { if (got.length && alive.current) setFiles((f) => (f[field]?.length ? f : { ...f, [field]: got })); });
  }, [cmd?.name]);
  return {
    values: { ...(server ?? {}), ...edited },
    files,
    edit: (name: string, v: unknown) => setEdited((o) => ({ ...o, [name]: v })),
    attach: (name: string, f: File[]) => setFiles((o) => ({ ...o, [name]: f })),
    forget: (names: string[]) => setEdited((o) => Object.fromEntries(Object.entries(o).filter(([k]) => !names.includes(k)))),
    reset: () => { setEdited({}); setFiles({}); },
  };
}
