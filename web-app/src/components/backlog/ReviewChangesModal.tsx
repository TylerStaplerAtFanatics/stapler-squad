"use client";
// +feature: backlog:review-changes-modal

import { useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import { FilesTab } from "@/components/sessions/FilesTab";
import { getApiBaseUrl } from "@/lib/config";
import {
  backdrop,
  modal,
  modalHeader,
  modalTitle,
  modalLabel,
  closeButton,
  modalBody,
  openTerminalLink,
} from "./ReviewChangesModal.css";

interface ReviewChangesModalProps {
  sessionId: string;
  sessionTitle?: string;
  onClose: () => void;
}

export function ReviewChangesModal({ sessionId, sessionTitle, onClose }: ReviewChangesModalProps) {
  const modalRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      }
    }
    window.addEventListener("keydown", handleKeyDown, { capture: true });
    return () => window.removeEventListener("keydown", handleKeyDown, { capture: true });
  }, [onClose]);

  useEffect(() => {
    modalRef.current?.focus();
  }, []);

  if (typeof document === "undefined") return null;

  return createPortal(
    <>
      <div className={backdrop} onClick={onClose} aria-hidden="true" />
      <div
        ref={modalRef}
        className={modal}
        role="dialog"
        aria-modal="true"
        aria-labelledby="review-changes-title"
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
      >
        <div className={modalHeader}>
          <span id="review-changes-title" className={modalTitle}>
            {sessionTitle ?? sessionId}
          </span>
          <span className={modalLabel}>Changes</span>
          <a
            className={openTerminalLink}
            href={`/?session=${sessionId}`}
            title="Open session in terminal"
          >
            Open in Terminal ↗
          </a>
          <button className={closeButton} onClick={onClose} aria-label="Close changes viewer">
            ✕
          </button>
        </div>
        <div className={modalBody}>
          <FilesTab sessionId={sessionId} baseUrl={getApiBaseUrl()} />
        </div>
      </div>
    </>,
    document.body,
  );
}
