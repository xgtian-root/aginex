"use client";

import { useEffect, useId, useRef } from "react";

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel,
  busy = false,
  destructive = false,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  title: string;
  description: React.ReactNode;
  confirmLabel: string;
  cancelLabel: string;
  busy?: boolean;
  destructive?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const titleId = useId();
  const descriptionId = useId();

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;

    if (open && !dialog.open) {
      returnFocusRef.current =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null;
      if (typeof dialog.showModal === "function") {
        dialog.showModal();
      } else {
        dialog.setAttribute("open", "");
      }
      cancelRef.current?.focus();
      return;
    }

    if (!open && dialog.open) {
      if (typeof dialog.close === "function") {
        dialog.close();
      } else {
        dialog.removeAttribute("open");
      }
      returnFocusRef.current?.focus();
      returnFocusRef.current = null;
    }
  }, [open]);

  return (
    <dialog
      aria-describedby={descriptionId}
      aria-labelledby={titleId}
      className="confirm-dialog"
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onCancel();
      }}
      ref={dialogRef}
    >
      <div className="confirm-dialog__body">
        <h2 id={titleId}>{title}</h2>
        <div id={descriptionId}>{description}</div>
      </div>
      <div className="confirm-dialog__actions">
        <button
          className="button secondary"
          disabled={busy}
          onClick={onCancel}
          ref={cancelRef}
          type="button"
        >
          {cancelLabel}
        </button>
        <button
          className={destructive ? "button danger" : "button"}
          disabled={busy}
          onClick={onConfirm}
          type="button"
        >
          {confirmLabel}
        </button>
      </div>
    </dialog>
  );
}
