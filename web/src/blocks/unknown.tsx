import { z } from 'zod';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Alert, Stack, Text, UnstyledButton } from '@mantine/core';
import { useTranslation } from 'react-i18next';

export const UNKNOWN = 'deck.unknown';

// unknown stands for a block this client does not know: the web client of a newer deck may.
export const unknown = createComponentImplementation({ name: UNKNOWN, schema: z.object({ type: z.string() }) }, ({ props }) => {
  const { t } = useTranslation();
  return (
    <Alert color="gray" data-unknown={props.type}>
      <Stack gap="xs" align="flex-start">
        <Text size="sm">{t('screen.unknown', { type: props.type })}</Text>
        <UnstyledButton component="a" href={location.href} onClick={() => location.reload()}>
          <Text c="blue" size="sm">{t('screen.open_in_web')}</Text>
        </UnstyledButton>
      </Stack>
    </Alert>
  );
});
