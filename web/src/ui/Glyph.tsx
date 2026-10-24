import { IconActivity, IconSparkles } from '@tabler/icons-react';

// Glyph is the mark of the master: sparkles when it rests, the sign of work while it works; it does not move.
export function Glyph({ busy }: { busy: boolean }) {
  return busy ? <IconActivity size={14} data-glyph-busy /> : <IconSparkles size={14} />;
}
