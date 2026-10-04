import type { Shown } from '../../lib/format';
import { DataTable } from '../../table';
import { blockImpl } from '../blockImpl';

export const table = blockImpl<{ columns: (Shown & { label: string })[] }>('table', (p, d, context) => <DataTable columns={p.columns} rows={d.rows ?? []} context={context} />);
