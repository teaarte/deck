import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import s from '../shell.module.css';
import { DialogClose } from '../screen/dialogContext';
import { dropParam, setParam, useRoute } from '../app/router';
import { NamedIcon } from '../ui/NamedIcon';
import { api, kids } from './blockImpl';

// dialog is a button of the screen that opens its blocks in a window; the open one is in the address.
export const dialog = createComponentImplementation(api('dialog'), ({ props, buildChild }) => {
  const p = props as { title?: string; icon?: string; name: string; primary?: boolean; children: unknown };
  const route = useRoute();
  const open = route.params?.dialog === p.name;
  const close = () => dropParam('dialog');
  return (
    <>
      <Button variant={p.primary ? 'filled' : 'default'} radius="xl" data-dialog-open={p.name} onClick={() => setParam('dialog', p.name)}
        leftSection={p.icon ? <NamedIcon name={p.icon} fallback={p.title ?? ''} size={16} /> : undefined}>{p.title}</Button>
      <Modal opened={open} onClose={close} size="lg" radius="lg" data-dialog={p.name} className={s.dialog}
        title={<Group gap={8}>{p.icon && <NamedIcon name={p.icon} fallback={p.title ?? ''} size={20} />}<Text fw={700} size="lg">{p.title}</Text></Group>}>
        <DialogClose.Provider value={close}>
          {open && <Stack gap="md">{kids(p.children, buildChild)}</Stack>}
        </DialogClose.Provider>
      </Modal>
    </>
  );
});
