import { invoke, fetchFile, saveBlob, type Effect } from '../lib/api';
import type { Action } from '../lib/catalog.gen';
import { openTopic } from '../chat/chatStore';
import { go } from '../app/router';
import { watchRun } from '../ui/actions/pressStore';

// Done is what a call of a pressed button gave: its data and what it did.
export interface Done { result?: unknown; effect?: Effect }

// act does what a pressed button means and gives the answer of its call, made with the key of the press; a failed call
// throws. A chat button opens its topic, a download button downloads its file.
export async function act(c: Action, key?: string): Promise<Done> {
  if (c.chat && !c.command && !c.screen) {
    openTopic('new');
    return {};
  }
  if (c.screen) {
    const params = c.params as Record<string, string> | undefined;
    go({ project: c.project || undefined, screen: c.screen, record: c.record, params: params && Object.keys(params).length ? params : undefined });
    return {};
  }
  const { data, effect } = await invoke(c.command!, c.params ?? {}, key);
  const done = { result: data, effect };
  const o = outcome(done);
  if (o.started) void watchRun(o.started);
  if (c.chat && o.topic) openTopic(o.topic);
  if (c.download && o.file) saveBlob(await fetchFile('/api/files/' + o.file.id), o.file.name);
  return done;
}

// Outcome is what an answer of a call names: the process it started, or joined as it was running already, the topic
// of the chat it made, the file it made.
export interface Outcome { started?: number; running?: boolean; topic?: number; file?: { id: number; name: string } }

// outcome reads the answer of a call, one way for every caller: deck says a call started a process, whose id its data
// holds, and a run already going says so beside it.
export function outcome(d: Done): Outcome {
  const o = (d.result && typeof d.result === 'object' ? d.result : {}) as Record<string, unknown>;
  const run = d.effect === 'started' && typeof o.process === 'number';
  return {
    started: run ? o.process as number : undefined,
    running: run && o.running === true,
    topic: typeof o.topic === 'number' ? o.topic : undefined,
    file: typeof o.file === 'number' ? { id: o.file, name: typeof o.name === 'string' ? o.name : 'file' } : undefined,
  };
}
