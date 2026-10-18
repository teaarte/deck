import { useState } from 'react';
import { ActionIcon, Button, Group, Text, Tooltip } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconArrowsDiagonal, IconArrowsDiagonalMinimize2, IconMessageCircle, IconPlayerPlayFilled } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { setChatOpen } from '../chat/chatStore';
import { useMenuData } from '../hooks/useMenu';
import { setLook, useLook } from '../theme/look';
import { Deki, useMascot, type Mood } from '../ui/deki/Deki';
import s from './hero.module.css';
import { Triage } from './Triage';

// MOODS are the poses of Deki for each state of the pulse; a calm one rests under the sky.
const MOODS: Record<string, Mood> = { asks: 'wait', running: 'work', errors: 'think', talking: 'dots' };

// Hero is the top of the cockpit: the pulse deck gives in a word, Deki in the pose of it, the way through the
// decisions one by one and to the master; folded, and always on a phone, it is one line with Deki whole.
export function Hero() {
  const { t } = useTranslation();
  const { pulse } = useMenuData();
  const { slim: folded, sky } = useLook();
  const phone = useMediaQuery('(max-width: 48em)') ?? false;
  const slim = folded || phone;
  const [triage, setTriage] = useState(false);
  const mascot = useMascot();
  const { asks, running, state } = pulse;
  const mood: Mood = MOODS[state] ?? (sky ? 'chill' : 'sleepy');
  const eyebrow = asks || running ? t('hero.on_shift', { count: running }) : t('hero.calm_eyebrow');
  const title = t('hero.' + state, { count: { asks, running, errors: pulse.errors, talking: running, calm: 0 }[state] });
  const text = t('hero.' + state + '_text');
  const talk = () => {
    setChatOpen(true);
    setTimeout(() => document.querySelector<HTMLTextAreaElement>('[data-composer] textarea')?.focus(), 150);
  };
  const go = asks > 0 && (
    <Button size={slim ? 'xs' : 'sm'} radius="md" leftSection={<IconPlayerPlayFilled size={14} />} onClick={() => setTriage(true)} data-triage-open>
      {phone ? t('hero.triage_short') : t('hero.triage')}
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
        <div className={`${s.hero} ${s.slim}`} data-hero="slim" data-hero-state={state} data-calm={(state === 'calm' && sky) || undefined}>
          {mascot && <span className={s.slimArt}><Deki mood={mood} size={44} still /></span>}
          <Text fw={700} fz={15} truncate>{title}</Text>
          <Text fz={13} c="dimmed" truncate className={s.slimEyebrow}>{eyebrow}</Text>
          <Group gap={6} wrap="nowrap" ml="auto">{go}{!phone && fold}</Group>
        </div>
      ) : (
        <div className={s.hero} data-hero="full" data-hero-state={state} data-no-sky={!sky || undefined}>
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
