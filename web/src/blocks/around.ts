import { createContext } from 'react';

// Around is the title of the tab or the dialog a block is in, which the title of the block does not say again.
export const Around = createContext<string | undefined>(undefined);

// Small is set inside a card of the tiles: its blocks go without their frames.
export const Small = createContext(false);
