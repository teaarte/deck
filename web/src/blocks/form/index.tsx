import { Form, type FormValue } from './Form';
import { blockImpl } from '../blockImpl';

export const form = blockImpl('form', (_, d) => <Form value={d.value as FormValue} />);
