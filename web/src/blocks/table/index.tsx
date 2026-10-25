import type { Shown } from '../../lib/format';
import { DataTable } from './DataTable';
import { blockImpl } from '../blockImpl';

export const table = blockImpl<{ columns: (Shown & { label: string })[] }>('table', (p, d) => <DataTable columns={p.columns} rows={d.rows ?? []} searched={d.filters?.some((f) => f.search)} />);
