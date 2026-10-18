import { useState } from 'react';
import { ActionIcon, Button, Group, Text, Tooltip } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconArrowsDiagonal, IconArrowsDiagonalMinimize2, IconMessageCircle, IconPlayerPlayFilled } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { setChatOpen } from '../chat/chatStore';
import { useMenuData, type HeroLine } from '../hooks/useMenu';
import type { Action } from '../lib/catalog.gen';
import { setLook, useLook } from '../theme/look';
import { ActionButton } from '../ui/actions/ActionButton';
import { Deki, useMascot, type Mood } from '../ui/deki/Deki';
import s from './hero.module.css';
import { Triage } from './Triage';

// MOODS are the poses of Deki for each state of the pulse; a calm one rests under the sky.
const MOODS: Record<string, Mood> = { asks: 'wait', running: 'work', errors: 'think', talking: 'dots' };

// Lines are the counts deck gives for the top of the cockpit, each opening its screen, some with the button that
// clears them at once.
function Lines({ lines }: { lines: HeroLine[] }) {
  if (!lines.length) return null;
  return (
    <div className={s.lines} data-hero-lines>
      {lines.map((l) => (
        <span key={l.text} className={s.line} data-tone={l.tone || undefined} data-hero-line>
          <span className={s.dot} data-tone={l.tone || undefined} />
          {l.link ? <a href={l.link} className={s.lineText}>{l.text}</a> : <span className={s.lineText}>{l.text}</span>}
          {l.action && <ActionButton act={l.action as Action} />}
        </span>
      ))}
    </div>
  );
}

// Hero is the top of the cockpit as deck writes it from what runs, failed and waits, Deki in the pose of it, the way
// through the decisions one by one and to the master; folded, and always on a phone, it is a line over its counts.
export function Hero() {
  const { t } = useTranslation();
  const { pulse } = useMenuData();
  const { slim: folded, sky } = useLook();
  const phone = useMediaQuery('(max-width: 48em)') ?? false;
  const slim = folded || phone;
  const [triage, setTriage] = useState(false);
  const mascot = useMascot();
  const { asks, state } = pulse;
  const lines = pulse.lines ?? [];
  const mood: Mood = MOODS[state] ?? (sky ? 'chill' : 'sleepy');
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
  return (
    <>
      {slim ? (
        <div className={`${s.hero} ${s.slim}`} data-hero="slim" data-hero-state={state} data-calm={(state === 'calm' && sky) || undefined}>
          <div className={s.slimHead}>
            {mascot && <span className={s.slimArt}><Deki mood={mood} size={44} still /></span>}
            <Text fw={700} fz={15} truncate data-hero-title>{pulse.title}</Text>
            <Group gap={6} wrap="nowrap" ml="auto">{go}{!phone && fold}</Group>
          </div>
          <Lines lines={lines} />
        </div>
      ) : (
        <div className={s.hero} data-hero="full" data-hero-state={state} data-no-sky={!sky || undefined}>
          <div className={s.words}>
            <Text className={s.title} data-hero-title>{pulse.title}</Text>
            <Text className={s.text}>{pulse.text}</Text>
            <Lines lines={lines} />
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
