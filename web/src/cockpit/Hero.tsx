import { useState, type ReactNode } from 'react';
import { ActionIcon, Button, Group, Text, Tooltip } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconArrowsDiagonal, IconArrowsDiagonalMinimize2, IconPlayerPlayFilled } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useMenuData, type HeroLine } from '../hooks/useMenu';
import type { Action } from '../lib/catalog.gen';
import { setLook, useLook } from '../theme/look';
import { useWorn } from '../theme/worn';
import { ActionButton } from '../ui/actions/ActionButton';
import { Deki, moodOf, useMascot } from '../ui/deki/Deki';
import s from './hero.module.css';
import { Triage } from './Triage';

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

// Hero is the top of the cockpit as deck writes it, Deki in the pose of it, the way through the decisions and the
// dialogs of the cockpit under its words; folded, and always on a phone, it is a line over its counts.
export function Hero({ dialogs }: { dialogs?: ReactNode }) {
  const { t } = useTranslation();
  const { pulse } = useMenuData();
  const { slim: folded } = useLook();
  const sky = useWorn().backdrop === 'sky';
  const phone = useMediaQuery('(max-width: 48em)') ?? false;
  const slim = folded || phone;
  const [triage, setTriage] = useState(false);
  const mascot = useMascot();
  const { asks, state } = pulse;
  const lines = pulse.lines ?? [];
  const mood = moodOf(state, sky);
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
            <Group gap={6} wrap="nowrap" ml="auto" data-hero-dialogs>{go}{dialogs}{!phone && fold}</Group>
          </div>
          <Lines lines={lines} />
        </div>
      ) : (
        <div className={s.hero} data-hero="full" data-hero-state={state} data-no-sky={!sky || undefined}>
          <div className={s.words}>
            <Text className={s.title} data-hero-title>{pulse.title}</Text>
            <Text className={s.text}>{pulse.text}</Text>
            <Lines lines={lines} />
            {(go || dialogs) && <Group gap={8} className={s.actions} data-hero-dialogs>{dialogs}{go}</Group>}
          </div>
          <div className={s.art}><Deki mood={mood} size={mood === 'chill' ? 150 : 180} /></div>
          <div className={s.fold}>{fold}</div>
        </div>
      )}
      <Triage opened={triage} onClose={() => setTriage(false)} />
    </>
  );
}
