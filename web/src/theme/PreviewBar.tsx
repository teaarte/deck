import { useEffect, useRef } from 'react';
import { Button, Group, Text, useMantineColorScheme } from '@mantine/core';
import { IconPalette } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useRoute } from '../app/router';
import { press } from '../screen/press';
import { notifyError } from '../lib/notify';
import { endPreview, shot, usePreview, useWorn } from './worn';
import s from './preview.module.css';

// PreviewBar says the page wears a theme of a link, not the one in use: Use puts it on this deck, Leave ends it, as
// does a move to another screen.
export function PreviewBar() {
  const { t } = useTranslation();
  const preview = usePreview();
  const worn = useWorn();
  const route = useRoute();
  const { setColorScheme } = useMantineColorScheme();
  const at = useRef(`${route.project}/${route.screen}/${route.record}`);
  useEffect(() => {
    const now = `${route.project}/${route.screen}/${route.record}`;
    if (now !== at.current) endPreview();
    at.current = now;
  }, [route.project, route.screen, route.record]);
  if ((!preview.theme && !preview.scheme) || shot) return null;
  const use = async () => {
    const r = preview.theme && worn.use ? await press(undefined, worn.use) : {};
    if (r.error) return notifyError(r.error, t('Theme'));
    if (preview.scheme) setColorScheme(preview.scheme);
    endPreview();
  };
  return (
    <Group className={s.bar} gap="sm" justify="center" wrap="wrap" role="status" data-preview-bar>
      <IconPalette size={16} />
      <Text size="sm" fw={600}>{t('Preview of the theme “{{theme}}”', { theme: worn.title || preview.theme })}</Text>
      <Button size="compact-sm" onClick={() => void use()} data-preview="use">{worn.use?.label ?? t('Use')}</Button>
      <Button size="compact-sm" variant="default" onClick={endPreview} data-preview="leave">{t('Leave')}</Button>
    </Group>
  );
}
