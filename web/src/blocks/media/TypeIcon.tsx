import { Text, ThemeIcon } from '@mantine/core';
import { extOf, kindOf, type Item, type Kind } from './kinds';

const COLORS: Record<Kind, string> = { image: 'teal', svg: 'teal', video: 'grape', audio: 'pink', pdf: 'red', html: 'orange', text: 'blue', other: 'gray' };

// TypeIcon is a colored badge with the extension of a file, its label growing with it.
export function TypeIcon({ it, size = 36 }: { it: Item; size?: number }) {
  const label = (extOf(it.name) || 'file').slice(0, 4).toUpperCase();
  return (
    <ThemeIcon variant="light" color={COLORS[kindOf(it)]} size={size} radius={Math.max(2, Math.round(size * 0.14))} aria-hidden data-type-icon>
      <Text fw={700} style={{ fontSize: Math.max(8, Math.round(size * 0.27)), letterSpacing: -0.5 }}>{label}</Text>
    </ThemeIcon>
  );
}
