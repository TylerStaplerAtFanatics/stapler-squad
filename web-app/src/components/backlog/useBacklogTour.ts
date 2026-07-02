import { useState, useEffect, useCallback } from "react";

export const BACKLOG_ONBOARDED_KEY = "stapler-squad:backlog-onboarded";

/** First-visit walkthrough state for the backlog page. Mirrors useOnboarding.ts. */
export function useBacklogTour() {
  const [showTour, setShow] = useState(false);

  useEffect(() => {
    let timerId: ReturnType<typeof setTimeout>;
    try {
      if (!localStorage.getItem(BACKLOG_ONBOARDED_KEY)) {
        timerId = setTimeout(() => setShow(true), 500);
      }
    } catch {
      // ignore storage errors (private browsing mode, etc.)
    }
    return () => clearTimeout(timerId);
  }, []);

  const setTourComplete = useCallback(() => {
    try {
      localStorage.setItem(BACKLOG_ONBOARDED_KEY, "true");
    } catch {
      // ignore storage errors
    }
    setShow(false);
  }, []);

  const resetTour = useCallback(() => {
    try {
      localStorage.removeItem(BACKLOG_ONBOARDED_KEY);
    } catch {
      // ignore storage errors
    }
    setShow(true);
  }, []);

  return { showTour, setTourComplete, resetTour };
}
