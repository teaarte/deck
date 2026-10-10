import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { renderMarkdown } from '@a2ui/markdown-it';
import { ActionIcon, CopyButton, Tooltip, Typography } from '@mantine/core';
import { IconCheck, IconCopy } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import s from './markdown.module.css';

// CopyCode copies the code of the block it sits in.
function CopyCode({ host }: { host: HTMLElement }) {
  const { t } = useTranslation();
  const code = host.parentElement?.querySelector('code')?.textContent ?? host.parentElement?.textContent ?? '';
  return (
    <CopyButton value={code}>{({ copied, copy }) => (
      <Tooltip label={copied ? t('actions.copied') : t('actions.copy')}>
        <ActionIcon variant="default" size="sm" onClick={copy} aria-label={t('actions.copy')} data-copy-code>
          {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
        </ActionIcon>
      </Tooltip>
    )}</CopyButton>
  );
}

// Markdown draws markdown text as HTML the renderer has sanitized; a block of code has a copy button, shown on hover
// or on a tap.
export function Markdown({ text }: { text: string }) {
  const [html, setHtml] = useState('');
  const [hosts, setHosts] = useState<HTMLElement[]>([]);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let live = true;
    void renderMarkdown(text).then((h) => { if (live) setHtml(h); });
    return () => { live = false; };
  }, [text]);
  useLayoutEffect(() => {
    const made = [...(box.current?.querySelectorAll('pre') ?? [])].map((pre) => {
      pre.tabIndex = -1;
      const host = document.createElement('span');
      host.className = s.codeTools;
      pre.appendChild(host);
      return host;
    });
    setHosts(made);
    return () => made.forEach((h) => h.remove());
  }, [html]);
  return (
    <Typography data-markdown className={s.text}>
      <div ref={box} dangerouslySetInnerHTML={{ __html: html }} />
      {hosts.map((h, i) => createPortal(<CopyCode host={h} />, h, i))}
    </Typography>
  );
}
