import { Form, type FormValue } from '../../form';
import { blockImpl } from '../blockImpl';

export const form = blockImpl('form', (_, d) => <Form value={d.value as FormValue} />);
