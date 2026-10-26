import { call, fetchFile, saveBlob } from '../lib/api';
import type { Action } from '../lib/catalog.gen';
import { openTopic } from '../chat/chatStore';
import { go } from '../app/router';
import { watchRun } from '../ui/actions/pressStore';

// act does what a pressed button means and gives the answer of its call; a chat button opens its topic, a download
// button downloads its file, and what a run it started waits to make comes to the owner. A failed call throws.
export async function act(c: Action): Promise<unknown> {
  if (c.chat && !c.command && !c.screen) {
    openTopic('new');
    return undefined;
  }
  if (c.screen) {
    const params = c.params as Record<string, string> | undefined;
    go({ project: c.project || undefined, screen: c.screen, record: c.record, params: params && Object.keys(params).length ? params : undefined });
    return undefined;
  }
  const out = await call(c.command!, c.params ?? {});
  const o = outcome(out);
  if (o.started) void watchRun(o.started);
  if (c.chat && o.topic) openTopic(o.topic);
  if (c.download && o.file) saveBlob(await fetchFile('/api/files/' + o.file.id), o.file.name);
  return out;
}

// Outcome is what an answer of a call names: the process it started, or joined as it was running already, the topic
// of the chat it made, the file it made.
export interface Outcome { started?: number; running?: boolean; topic?: number; file?: { id: number; name: string } }

// outcome reads the answer of a call, one way for every caller: a started process is an answer of its id alone, a run
// already going says so beside it.
export function outcome(out: unknown): Outcome {
  const o = (out && typeof out === 'object' ? out : {}) as Record<string, unknown>;
  const run = typeof o.process === 'number' && Object.keys(o).every((k) => k === 'process' || k === 'running');
  return {
    started: run ? o.process as number : undefined,
    running: run && o.running === true,
    topic: typeof o.topic === 'number' ? o.topic : undefined,
    file: typeof o.file === 'number' ? { id: o.file, name: typeof o.name === 'string' ? o.name : 'file' } : undefined,
  };
}
