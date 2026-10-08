import { useState } from 'react';
import { Code, Collapse, Stack, UnstyledButton } from '@mantine/core';
import { IconChevronDown } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import s from './details.module.css';

// Details holds a long text folded under a button, or open from the start; of names what it is in the texts of the
// button, a call by default.
export function Details({ text, of = 'call', open: start }: { text: string; of?: 'call' | 'text'; open?: boolean }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(!!start);
  return (
    <Stack gap={6} align="flex-start" w="100%">
      <UnstyledButton className={s.toggle} onClick={() => setOpen(!open)} aria-expanded={open} data-details>
        {open ? t(`details.${of}.hide`) : t(`details.${of}.show`)}<IconChevronDown size={14} className={s.chevron} data-open={open || undefined} />
      </UnstyledButton>
      <Collapse expanded={open} w="100%"><Code block className={s.text}>{text}</Code></Collapse>
    </Stack>
  );
}
