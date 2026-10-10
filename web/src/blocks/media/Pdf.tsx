import { useEffect, useRef, useState } from 'react';
import { Alert, Loader, Stack } from '@mantine/core';
import type { Item } from './kinds';
import { useBlob } from './useBlob';

// PAGES is how many pages of a PDF are drawn in place; the rest is in the download.
const PAGES = 10;

// Pdf draws the pages of a PDF on canvases with pdf.js: no frame, so the page of deck frames nothing.
export function Pdf({ it }: { it: Item }) {
  const { blob, error } = useBlob(it.url, 'application/pdf');
  const box = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState('');
  useEffect(() => {
    const el = box.current;
    if (!blob || !el) return;
    let gone = false;
    let task: { destroy: () => Promise<void> } | undefined;
    void (async () => {
      try {
        const pdfjs = await import('pdfjs-dist');
        pdfjs.GlobalWorkerOptions.workerSrc = (await import('pdfjs-dist/build/pdf.worker.min.mjs?url')).default;
        const loading = pdfjs.getDocument({ data: new Uint8Array(await blob.arrayBuffer()) });
        task = loading;
        const doc = await loading.promise;
        for (let n = 1; n <= Math.min(doc.numPages, PAGES) && !gone; n++) {
          const page = await doc.getPage(n);
          const width = el.clientWidth || 600;
          const viewport = page.getViewport({ scale: (width / page.getViewport({ scale: 1 }).width) * devicePixelRatio });
          const canvas = document.createElement('canvas');
          canvas.width = viewport.width;
          canvas.height = viewport.height;
          canvas.style.width = '100%';
          canvas.style.border = '1px solid var(--mantine-color-default-border)';
          canvas.dataset.page = String(n);
          el.appendChild(canvas);
          await page.render({ canvas, viewport }).promise;
        }
      } catch (e) {
        if (!gone) setFailed((e as Error).message);
      }
    })();
    return () => { gone = true; void task?.destroy(); el.replaceChildren(); };
  }, [blob]);
  if (error || failed) return <Alert color="red" p="xs">{error || failed}</Alert>;
  if (!blob) return <Loader size="xs" />;
  return <Stack ref={box} gap="xs" data-media="pdf" aria-label={it.title || it.name} />;
}
