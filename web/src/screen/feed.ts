import i18n from '../lib/i18n';
import { known, UNKNOWN } from './catalog';

export const VERSION = 'v0.9';

export type Message = Record<string, any> & { version?: string };

// prepare checks the protocol version and swaps components the client does not know for open in web.
export function prepare(msgs: Message[]): Message[] {
  for (const m of msgs) {
    if (m.version !== VERSION) throw new Error(i18n.t('screen.version', { got: m.version ?? '?', want: VERSION }));
  }
  return msgs.map((m) => {
    if (!m.updateComponents) return m;
    const components = (m.updateComponents.components as Message[]).map((c) =>
      known.has(c.component) ? c : { id: c.id, component: UNKNOWN, type: String(c.component) });
    return { ...m, updateComponents: { ...m.updateComponents, components } };
  });
}

// Feed hands the reads of a screen to its processor. Every read takes a ticket; the data of a block is never
// replaced by a read older than the one it shows.
export class Feed {
  private tickets = 0;
  private fresh: Record<string, number> = {};

  constructor(private process: (m: Message) => void) {}

  ticket = () => ++this.tickets;

  // drawn says whether a whole screen was read once.
  get drawn() { return this.fresh.root !== undefined; }

  reset() { this.fresh = {}; }

  // whole feeds a read of the whole screen.
  whole(msgs: Message[], ticket: number) {
    this.feed(msgs, ticket);
    this.fresh.root = ticket;
  }

  feed(msgs: Message[], ticket: number) {
    const fresh = this.fresh;
    for (const m of prepare(msgs)) {
      if (m.createSurface && fresh.root !== undefined) continue;
      if (m.updateComponents) {
        if ((fresh.components ?? 0) > ticket) continue;
        fresh.components = ticket;
      }
      const id = (m.updateDataModel?.path as string | undefined)?.slice(1);
      if (id && (fresh[id] ?? 0) > ticket) continue;
      if (id) fresh[id] = ticket;
      this.process(m);
    }
  }
}
