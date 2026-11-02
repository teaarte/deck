import { createContext } from 'react';
import catalogSchema from '../lib/catalogSchema';

// Around is the title of the tab or the dialog a block is in, which the title of the block does not say again.
export const Around = createContext<string | undefined>(undefined);

// Small is set inside a card of the tiles: its blocks go without their frames.
export const Small = createContext(false);

// Fit is set inside a widget: its blocks take their thirds of a line of the card (internal/screen/fit.go), each
// line one row of words, so nothing goes out of the card.
export const Fit = createContext(false);

// thirds is what a card of h rows of cells holds under its head, by the numbers of the catalog the server counts by.
export const thirds = (h: number) => catalogSchema.widget.row * h - catalogSchema.widget.head;
