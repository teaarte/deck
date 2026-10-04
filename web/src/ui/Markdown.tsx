import { useEffect, useState } from 'react';
import { Typography } from '@mantine/core';
import { renderMarkdown } from '@a2ui/markdown-it';

// Markdown draws markdown text as HTML the renderer has sanitized.
export function Markdown({ text }: { text: string }) {
  const [html, setHtml] = useState('');
  useEffect(() => {
    let live = true;
    void renderMarkdown(text).then((h) => { if (live) setHtml(h); });
    return () => { live = false; };
  }, [text]);
  return <Typography data-markdown><div dangerouslySetInnerHTML={{ __html: html }} /></Typography>;
}
