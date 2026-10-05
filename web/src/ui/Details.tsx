import { useState } from 'react';
import { Anchor, Code, Collapse, Stack } from '@mantine/core';
import { useTranslation } from 'react-i18next';

// Details holds a long text folded under a link; of names what it is in the texts of the link, a call by default.
export function Details({ text, of = 'call' }: { text: string; of?: 'call' | 'text' }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <Stack gap={4} align="flex-start">
      <Anchor component="button" size="xs" onClick={() => setOpen(!open)}>{open ? t(`details.${of}.hide`) : t(`details.${of}.show`)}</Anchor>
      <Collapse expanded={open} w="100%"><Code block>{text}</Code></Collapse>
    </Stack>
  );
}
