import { useState } from 'react';
import { Loader, Select } from '@mantine/core';
import type { Action } from '../../lib/catalog.gen';
import { show } from '../../lib/format';
import { choiceData, choices, factsOf } from '../../ui/actions/ActionInput';
import { Failed } from '../../ui/actions/Failed';
import { usePress } from '../../ui/actions/usePress';
import { Logo } from '../../ui/logo/Logo';
import { Mark, OptionLine } from '../../ui/OptionLine';

// Pick is a cell that is the input of a button of its row: it shows the value by its label, and a pick presses the
// button with it.
export function Pick({ act, value, logo }: { act: Action; value: unknown; logo?: boolean }) {
  const { busy, label, send, error, clear } = usePress(act);
  // The value picked shows at once, with a loader while it goes.
  const [picked, setPicked] = useState('');
  const now = busy && picked ? picked : show(value);
  const opts = choices(act);
  const facts = factsOf(act);
  const data = opts.some((o) => o.value === now) || !now ? choiceData(act) : [{ value: now, label: now }, ...choiceData(act)];
  return (
    <Failed error={error} onClose={clear}>
      <div onClick={(e) => e.stopPropagation()}>
        <Select size="xs" variant="filled" data={data} value={now || null} aria-label={label} disabled={busy} allowDeselect={false}
          rightSection={busy ? <Loader size={14} /> : undefined} data-busy={busy || undefined}
          leftSection={facts.get(now)?.logo ? <Mark facts={facts.get(now)} /> : logo && now ? <Logo names={now} size={16} /> : undefined}
          renderOption={({ option }) => <OptionLine label={option.label} facts={facts.get(option.value) ?? (logo ? { logo: option.value } : undefined)} />}
          comboboxProps={{ withinPortal: true, width: 'max-content' }} miw={140} maw={280} data-pick={act.pick}
          onChange={(v) => { if (v && v !== now) { setPicked(v); void send({ [act.input!]: v }); } }} />
      </div>
    </Failed>
  );
}
