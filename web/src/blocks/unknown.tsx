import { Catalog } from '@a2ui/web_core/v0_9';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Alert, Stack, Text, UnstyledButton } from '@mantine/core';
import { useTranslation } from 'react-i18next';

export const UNKNOWN = 'deck.unknown';

// unknownApi is the component of a block this client does not know, loaded as the catalog's own are.
const unknownApi = Catalog.fromSchema({ catalogId: UNKNOWN, components: { [UNKNOWN]: { type: 'object', properties: { type: { type: 'string' } } } } })
  .components.get(UNKNOWN)!;

// unknown stands for a block this client does not know: the web client of a newer deck may.
export const unknown = createComponentImplementation(unknownApi, ({ props }) => {
  const { t } = useTranslation();
  const type = String((props as { type?: unknown }).type ?? '');
  return (
    <Alert color="gray" data-unknown={type}>
      <Stack gap="xs" align="flex-start">
        <Text size="sm">{t('screen.unknown', { type })}</Text>
        <UnstyledButton component="a" href={location.href} onClick={() => location.reload()}>
          <Text c="blue" size="sm">{t('screen.open_in_web')}</Text>
        </UnstyledButton>
      </Stack>
    </Alert>
  );
});
