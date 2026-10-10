import { useState } from 'react';
import { ActionIcon, Button, Group, Text, Tooltip } from '@mantine/core';
import { IconArrowsDiagonal, IconArrowsDiagonalMinimize2, IconMessageCircle, IconPlayerPlayFilled } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { setChatOpen } from '../chat/chatStore';
import { useMenuEntries } from '../hooks/useMenu';
import { setLook, useLook } from '../theme/look';
import { Deki, DekiHead, useMascot, type Mood } from '../ui/deki/Deki';
import s from './hero.module.css';
import { Triage } from './Triage';

const sum = (xs: (number | undefined)[]) => xs.reduce<number>((a, x) => a + (x ?? 0), 0);

// Hero is the top of the cockpit: what waits for the owner and what runs in a word, Deki in the pose of it, the way
// through the decisions one by one and to the master; folded it is one line.
export function Hero() {
  const { t } = useTranslation();
  const menu = useMenuEntries();
  const { slim, sky } = useLook();
  const [triage, setTriage] = useState(false);
  const mascot = useMascot();
  const asks = sum(menu.map((m) => m.asks));
  const running = sum(menu.map((m) => m.running));
  const errors = sum(menu.map((m) => m.errors));
  const mood: Mood = asks ? 'wait' : running ? 'work' : errors ? 'think' : sky ? 'chill' : 'sleepy';
  const eyebrow = asks || running ? t('hero.on_shift', { count: running }) : t('hero.calm_eyebrow');
  const title = asks ? t('hero.asks', { count: asks }) : errors ? t('hero.errors', { count: errors }) : running ? t('hero.running', { count: running }) : t('hero.calm');
  const text = asks ? t('hero.asks_text') : errors ? t('hero.errors_text') : running ? t('hero.running_text') : t('hero.calm_text');
  const talk = () => {
    setChatOpen(true);
    setTimeout(() => document.querySelector<HTMLTextAreaElement>('[data-composer] textarea')?.focus(), 150);
  };
  const go = asks > 0 && (
    <Button size={slim ? 'xs' : 'sm'} radius="md" leftSection={<IconPlayerPlayFilled size={14} />} onClick={() => setTriage(true)} data-triage-open>
      {t('hero.triage')}
    </Button>
  );
  const fold = (
    <Tooltip label={slim ? t('hero.unfold') : t('hero.fold')}>
      <ActionIcon variant="subtle" color="gray" radius="xl" size={30} onClick={() => setLook({ slim: !slim })} aria-label={slim ? t('hero.unfold') : t('hero.fold')} data-hero-fold>
        {slim ? <IconArrowsDiagonal size={15} /> : <IconArrowsDiagonalMinimize2 size={15} />}
      </ActionIcon>
    </Tooltip>
  );
  const dot = <span className={s.dot} data-waits={asks > 0 || undefined} />;
  return (
    <>
      {slim ? (
        <div className={`${s.hero} ${s.slim}`} data-hero="slim">
          <DekiHead size={34} />
          <Text fw={700} fz={15} truncate>{title}</Text>
          <Text fz={13} c="dimmed" truncate className={s.slimEyebrow}>{eyebrow}</Text>
          <Group gap={6} wrap="nowrap" ml="auto">{go}{fold}</Group>
        </div>
      ) : (
        <div className={s.hero} data-hero="full" data-no-sky={!sky || undefined}>
          <div className={s.words}>
            <span className={s.eyebrow}>{dot}{eyebrow}</span>
            <Text className={s.title}>{title}</Text>
            <Text className={s.text}>{text}</Text>
            <Group gap={8} mt={6}>
              {go}
              <Button size="sm" radius="md" variant="white" color="dark" className={s.talk} leftSection={<IconMessageCircle size={15} />} onClick={talk} data-hero-talk>
                {t(mascot ? 'hero.talk_deki' : 'hero.talk')}
              </Button>
            </Group>
          </div>
          <div className={s.art}><Deki mood={mood} size={mood === 'chill' ? 150 : 180} /></div>
          <div className={s.fold}>{fold}</div>
        </div>
      )}
      <Triage opened={triage} onClose={() => setTriage(false)} />
    </>
  );
}
