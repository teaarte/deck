// KEY keeps the name a device gives itself to deck, made once.
const KEY = 'deck-device';

// device is the name of this device for what deck keeps of each device, such as where its tiles stand.
export function device(): string {
  let id = localStorage.getItem(KEY);
  if (!id) {
    id = crypto.randomUUID?.() ?? Math.random().toString(36).slice(2) + Date.now().toString(36);
    localStorage.setItem(KEY, id);
  }
  return id;
}
