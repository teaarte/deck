import { useState } from 'react';
import { Button, PasswordInput, Stack, Text, Title } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { setToken } from '../lib/api';

// TokenPrompt takes the access token once on a device: an app on the home screen does not share the browser's storage.
export function TokenPrompt() {
  const { t } = useTranslation();
  const [value, setValue] = useState('');
  return (
    <Stack maw={420} mx="auto" mt="xl" p="md">
      <Title order={3}>deck</Title>
      <Text size="sm">{t('This device has no access token of deck yet. The URL deck prints when it starts ends with it.')}</Text>
      <PasswordInput label={t('Access token')} value={value} onChange={(e) => setValue(e.currentTarget.value)} data-token />
      <Button disabled={!value.trim()} onClick={() => { setToken(value.trim()); location.reload(); }}>{t('Open deck')}</Button>
    </Stack>
  );
}
