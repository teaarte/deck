import { useEffect } from 'react';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import { ModalApi } from '@a2ui/web_core/v0_9';
import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import s from './blocks.module.css';
import { DialogClose } from '../screen/dialogContext';
import { dropParam, setParam, useRoute } from '../app/router';
import { NamedIcon } from '../ui/NamedIcon';
import { readBlock } from '../screen/press';

// modal is a dialog: its trigger, a button of a text, opens its content in a window, whose blocks are read then; the
// open one is in the address by the name of its content.
export const modal = createComponentImplementation(ModalApi, ({ props, buildChild, context }) => {
  const get = (id?: string) => (id ? context.surfaceComponents.get(id)?.properties ?? {} : {}) as Record<string, unknown>;
  const trigger = get(props.trigger);
  const content = get(props.content);
  const title = String(get(trigger.child as string).text ?? '');
  const name = String(content.name ?? '');
  const icon = content.icon as string | undefined;
  const route = useRoute();
  const open = route.params?.dialog === name;
  const close = () => dropParam('dialog');
  // The blocks of a dialog are read once it opens.
  useEffect(() => { if (open) readBlock(context, 'dialog'); }, [open]);
  return (
    <>
      <Button variant={trigger.variant === 'primary' ? 'filled' : 'default'} radius="xl" data-dialog-open={name} onClick={() => setParam('dialog', name)}
        leftSection={icon ? <NamedIcon name={icon} fallback={title} size={16} /> : undefined}>{title}</Button>
      <Modal opened={open} onClose={close} size="lg" radius="lg" data-dialog={name} className={s.dialog}
        title={<Group gap={8}>{icon && <NamedIcon name={icon} fallback={title} size={20} />}<Text fw={700} size="lg">{title}</Text></Group>}>
        <DialogClose.Provider value={close}>
          {open && <Stack gap="md">{buildChild(props.content)}</Stack>}
        </DialogClose.Provider>
      </Modal>
    </>
  );
});
