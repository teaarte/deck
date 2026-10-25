import { createComponentImplementation } from '@a2ui/react/v0_9';
import { ModalApi } from '@a2ui/web_core/v0_9';
import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import s from './blocks.module.css';
import { DialogClose } from '../screen/dialogContext';
import { dropParam, setParam, useRoute } from '../app/router';
import { NamedIcon } from '../ui/NamedIcon';
import { Around } from './around';

// modal is a dialog: its trigger, a button of a text, opens its content in a window; the open one is in the address
// by the name of its content.
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
  return (
    <>
      <Button variant={trigger.variant === 'primary' ? 'filled' : 'default'} data-dialog-open={name} onClick={() => setParam('dialog', name)}
        leftSection={icon ? <NamedIcon name={icon} fallback={title} size={16} /> : undefined}>{title}</Button>
      <Modal opened={open} onClose={close} size="lg" data-dialog={name} className={s.dialog} classNames={{ content: s.dialogWindow }}
        title={<Group gap={8}>{icon && <NamedIcon name={icon} fallback={title} size={20} />}<Text fw={700} size="lg">{title}</Text></Group>}>
        <DialogClose.Provider value={close}>
          {open && <Around.Provider value={title}><Stack gap="md">{buildChild(props.content)}</Stack></Around.Provider>}
        </DialogClose.Provider>
      </Modal>
    </>
  );
});
