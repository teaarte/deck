import { useEffect, useRef, useState } from 'react';
import { Button, CloseButton, Group, Modal, Text, UnstyledButton } from '@mantine/core';
import { IconArrowRight } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { Line, type Item } from '../blocks/list';
import { show } from '../lib/format';
import { Deki } from '../ui/Deki';
import s from './triage.module.css';
import { useDecisionLists } from './decisions';

const key = (it: Item) => show(it.id);

// Triage goes through the decisions of the top list one at a time: an answered one leaves the list and the next comes;
// skip puts the one shown at the end. Once that list is gone from the screen all is done.
export function Triage({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const { t } = useTranslation();
  const lists = useDecisionLists();
  const [list, setList] = useState('');
  const items = (opened && lists.find((l) => l.id === list)?.items) || [];
  const [queue, setQueue] = useState<string[]>([]);
  const seen = useRef(new Set<string>());
  useEffect(() => {
    if (!opened) return;
    const top = lists[0];
    const items = top?.items ?? [];
    setList(top?.id ?? '');
    seen.current = new Set(items.map(key));
    setQueue(items.map(key));
  }, [opened]);
  useEffect(() => {
    if (!opened) return;
    const fresh = items.map(key).filter((k) => !seen.current.has(k));
    if (!fresh.length) return;
    fresh.forEach((k) => seen.current.add(k));
    setQueue((q) => [...q, ...fresh]);
  }, [items, opened]);
  const live = new Map(items.map((it) => [key(it), it]));
  const left = queue.filter((k) => live.has(k));
  const current = left.length ? live.get(left[0]) : undefined;
  const total = seen.current.size;
  const done = total - left.length;
  return (
    <Modal opened={opened} onClose={onClose} fullScreen withCloseButton={false} padding={0} transitionProps={{ transition: 'fade', duration: 200 }}
      classNames={{ content: s.layer, body: s.body }} data-triage>
      <header className={s.head}>
        <Text fw={700} fz={15}>{t('triage.title')}</Text>
        {current && <Text fz={13} c="dimmed" data-triage-count>{t('triage.count', { n: done + 1, of: total })}</Text>}
        <div className={s.progress}>{Array.from({ length: total }, (_, i) => <span key={i} data-step={i < done ? 'done' : i === done ? 'now' : undefined} />)}</div>
        <CloseButton size={36} radius="xl" className={s.close} onClick={onClose} aria-label={t('triage.close')} data-triage-close />
      </header>
      <div className={s.center}>
        {current ? (
          <>
            <div className={s.deki}><Deki mood="wait" size={120} /></div>
            <div className={s.card} key={left[0]} data-triage-card>
              <Line it={current} big />
              <Group justify="flex-end" mt="md">
                {left.length > 1 && (
                  <UnstyledButton className={s.skip} onClick={() => setQueue([...left.slice(1), left[0]])} data-triage-skip>
                    {t('triage.skip')}<IconArrowRight size={15} />
                  </UnstyledButton>
                )}
              </Group>
            </div>
          </>
        ) : (
          <div className={s.done} data-triage-done>
            <Deki mood="chill" size={190} float />
            <Text fz={30} fw={750} lh={1.15}>{t('triage.done')}</Text>
            <Text c="dimmed" ta="center" maw={420}>{t('triage.done_text')}</Text>
            <Button size="md" radius="md" color="dark" onClick={onClose}>{t('triage.back')}</Button>
          </div>
        )}
      </div>
    </Modal>
  );
}
