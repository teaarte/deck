import { useState } from 'react';
import { Button } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { fetchFile, saveBlob } from '../../lib/api';
import { notifyError } from '../../lib/notify';
import type { Item } from './kinds';

// Download saves the file of an item through the browser.
export function Download({ it }: { it: Item }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  return (
    <Button size="xs" variant="default" loading={busy} data-download onClick={async () => {
      setBusy(true);
      try {
        saveBlob(await fetchFile(it.url!), it.name ?? 'file');
      } catch (e) {
        notifyError(e, it.title || it.name);
      } finally {
        setBusy(false);
      }
    }}>{t('verb.download')}</Button>
  );
}
