import { Select } from '@mantine/core';
import type { Action } from '../../lib/catalog.gen';
import { show } from '../../lib/format';
import { Failed } from '../../ui/actions/Failed';
import { useActionRun } from '../../ui/actions/useActionRun';

// Pick is a cell that is the input of a button of its row: it shows the value, and a pick presses the button with it.
export function Pick({ act, value }: { act: Action; value: unknown }) {
  const { busy, label, send, error, clear } = useActionRun(act);
  const now = show(value);
  const data = act.options?.includes(now) || !now ? act.options ?? [] : [now, ...(act.options ?? [])];
  return (
    <Failed error={error} onClose={clear}>
      <div onClick={(e) => e.stopPropagation()}>
        <Select size="xs" variant="filled" data={data} value={now || null} aria-label={label} disabled={busy} allowDeselect={false}
          comboboxProps={{ withinPortal: true, width: 'max-content' }} miw={140} maw={280} data-pick={act.pick}
          onChange={(v) => { if (v && v !== now) void send({ [act.input!]: v }); }} />
      </div>
    </Failed>
  );
}
