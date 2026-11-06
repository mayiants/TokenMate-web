import { Modal } from '@douyinfe/semi-ui';

/** Centered danger confirmation dialog for destructive/irreversible actions.
 *  Replaces inline Popconfirm per design system: danger confirmations use a
 *  centered custom modal (never native dialogs). */
export default function dangerConfirm(opts: {
  title: string;
  content?: string;
  okText: string;
  cancelText: string;
  onOk: () => void | Promise<unknown>;
}) {
  return Modal.confirm({
    title: opts.title,
    content: opts.content,
    okText: opts.okText,
    cancelText: opts.cancelText,
    okButtonProps: { type: 'danger', theme: 'solid' },
    onOk: opts.onOk,
  });
}
