import { call, fetchFile, saveBlob } from '../lib/api';
import type { Action } from '../lib/catalog.gen';
import { openTopic } from '../chat/chatStore';
import { go } from '../app/router';

// act runs a button: a move to its screen, or its call; a chat button opens the topic it made, a download button
// downloads the file it made. A failed call throws.
export async function act(c: Action): Promise<boolean> {
  if (c.chat && !c.command && !c.screen) {
    openTopic('new');
    return true;
  }
  if (c.screen) {
    const params = c.params as Record<string, string> | undefined;
    go({ project: c.project || undefined, screen: c.screen, record: c.record, params: params && Object.keys(params).length ? params : undefined });
    return true;
  }
  const out = await call(c.command!, c.params ?? {});
  const topic = (out as { topic?: unknown } | null)?.topic;
  if (c.chat && typeof topic === 'number') openTopic(topic);
  const made = out as { file?: unknown; name?: unknown } | null;
  if (c.download && typeof made?.file === 'number') {
    saveBlob(await fetchFile('/api/files/' + made.file), typeof made.name === 'string' ? made.name : 'file');
  }
  return true;
}
