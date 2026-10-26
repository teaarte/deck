import { createContext } from 'react';

// Around is the title of the tab or the dialog a block is in, which the title of the block does not say again.
export const Around = createContext<string | undefined>(undefined);

// Small is set inside a card of the tiles: its blocks go without their frames.
export const Small = createContext(false);

// Fit is set inside a widget: its blocks take their thirds of a line of the card (internal/screen/fit.go), each
// line one row of words, so nothing goes out of the card.
export const Fit = createContext(false);

// thirds is what a card of h rows of cells holds under its head, as the server counts it.
export const thirds = (h: number) => 9 * h - 3;
