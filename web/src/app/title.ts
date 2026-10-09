// The name of the tab: the count of new notifications, the screen and its project, then deck.
let fresh = 0;
let place = '';

const show = () => { document.title = (fresh ? `(${fresh}) ` : '') + (place ? place + ' · ' : '') + 'deck'; };

export function titleCount(n: number) { fresh = n; show(); }

export function titlePlace(p: string) { place = p; show(); }
