import { call } from '../lib/api';
import type { Action } from '../lib/catalog.gen';
import { openTopic } from '../chat/chatStore';
import { go } from '../app/router';

// act runs a button: a move to its screen, or its call; a chat button opens the topic it made. A failed call throws.
export async function act(c: Action): Promise<boolean> {
  if (c.screen) {
    const params = c.params as Record<string, string> | undefined;
    go({ project: c.project || undefined, screen: c.screen, record: c.record, params: params && Object.keys(params).length ? params : undefined });
    return true;
  }
  const out = await call(c.command!, c.params ?? {});
  const topic = (out as { topic?: unknown } | null)?.topic;
  if (c.chat && typeof topic === 'number') openTopic(topic);
  return true;
}
