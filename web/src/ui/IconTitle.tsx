import { Title, type TitleOrder } from '@mantine/core';
import s from '../shell.module.css';
import { NamedIcon } from './NamedIcon';

// IconTitle is the title of a screen or a block with its icon ahead; block marks the icon of a block.
export function IconTitle({ order, title, icon, size, className, block }: { order: TitleOrder; title: string; icon?: string; size: number; className: string; block?: boolean }) {
  return (
    <Title order={order} className={className}>
      {icon && <span className={s.titleIcon} data-block-icon={block || undefined}><NamedIcon name={icon} fallback={title} size={size} /></span>}{title}
    </Title>
  );
}
