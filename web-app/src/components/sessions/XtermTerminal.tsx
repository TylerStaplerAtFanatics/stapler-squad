"use client";

import { useEffect, useRef, useCallback, useImperativeHandle, forwardRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { WebglAddon } from "@xterm/addon-webgl";
import { CanvasAddon } from "@xterm/addon-canvas";
import { SearchAddon } from "@xterm/addon-search";
import "@xterm/xterm/css/xterm.css";
import styles from "./XtermTerminal.module.css";
import { loadTerminalConfig, darkTerminalTheme, lightTerminalTheme, type TerminalConfig } from "@/lib/config/terminalConfig";
import { isFiniteResizeDimensions, type ResizeDimensions } from "@/lib/terminal/types";

/**
 * Fixed cadence (ms) of the decoupled resize sampler. See
 * project_plans/terminal-resize-fit-loop/decisions/ADR-002-decoupled-sampler-tick-semantics.md
 */
const SAMPLE_INTERVAL_MS = 50;

/**
 * Bounded give-up threshold (~1s of sampling) for sustained oscillation. See
 * project_plans/terminal-resize-fit-loop/decisions/ADR-002-decoupled-sampler-tick-semantics.md
 */
const MAX_SAMPLES = 20;

/**
 * Tolerance (px) and consecutive-sample threshold for the WebGL actual-vs-
 * expected pixels-per-column mismatch tracker (AC5). Provisional values, not
 * yet validated against a real fractionally-scaled display — jsdom cannot
 * reproduce real WebGL glyph-width mismatch magnitude, especially under
 * fractional OS display scaling (Windows 125%/150%, macOS non-integer zoom),
 * which is exactly the condition that produces this mismatch in the first
 * place. Chosen as a reasonable starting point from requirements.md's own
 * "warns above a 1px tolerance" precedent, not measured data. See Task 5.2
 * step 7 in project_plans/terminal-resize-fit-loop/implementation/plan.md
 * for the real-device validation/tuning step.
 */
const MISMATCH_TOLERANCE_PX = 1;
const MISMATCH_THRESHOLD = 3;

export interface ShouldScheduleFitResult {
  schedule: boolean;
  nextPending: ResizeDimensions | null;
}

/**
 * Pure Reading-A dead-band decision: a fit() should only be scheduled once a
 * proposed candidate matches the immediately preceding sampled candidate
 * exactly (not merely "differs from applied"). See ADR-002 §2 for the full
 * derivation of why Reading A is correct and Reading B is not.
 */
export function shouldScheduleFit(
  proposed: ResizeDimensions | undefined,
  applied: ResizeDimensions,
  pending: ResizeDimensions | null
): ShouldScheduleFitResult {
  if (!proposed) return { schedule: false, nextPending: null };
  if (proposed.cols === applied.cols && proposed.rows === applied.rows) {
    return { schedule: false, nextPending: null };
  }
  if (pending && pending.cols === proposed.cols && pending.rows === proposed.rows) {
    return { schedule: true, nextPending: null };
  }
  return { schedule: false, nextPending: proposed };
}

export interface CellMismatchInputs {
  actualPxPerCol: number;
  expectedPxPerCol: number;
}

/**
 * Impure extraction of the raw actual-vs-expected pixels-per-column inputs
 * from xterm.js internals and DOM measurement (AC5). Returns null when the
 * renderer hasn't measured cell dimensions yet. Deliberately does no
 * Number.isFinite guarding here — `terminal.cols === 0` simply produces
 * `Infinity` and is passed through; `isSustainedMismatch()` is the sole
 * guard boundary (architecture-review.md Concern 2).
 */
export function extractCellMismatchInputs(
  terminal: Terminal,
  containerEl: HTMLElement
): CellMismatchInputs | null {
  const dims = (terminal as any)._core?._renderService?.dimensions;
  if (!dims?.css?.cell?.width) return null;
  return {
    actualPxPerCol: containerEl.getBoundingClientRect().width / terminal.cols,
    expectedPxPerCol: dims.css.cell.width,
  };
}

/**
 * Pure, Number.isFinite-guarded mismatch decision (AC5). Returns false
 * unless both inputs are finite — guards against the `terminal.cols === 0`
 * / hidden-tab `Infinity` case (pitfalls §4) using `Number.isFinite`, not
 * `Number.isNaN` (per AC5's explicit wording; `Number.isNaN(Infinity)` is
 * `false`, which would incorrectly admit the sample).
 */
export function isSustainedMismatch(
  actualPxPerCol: number,
  expectedPxPerCol: number,
  tolerance: number
): boolean {
  if (!Number.isFinite(actualPxPerCol) || !Number.isFinite(expectedPxPerCol)) {
    return false;
  }
  return Math.abs(actualPxPerCol - expectedPxPerCol) > tolerance;
}

export interface XtermTerminalProps {
  /**
   * Callback when user types in terminal
   */
  onData?: (data: string) => void;

  /**
   * Callback when terminal is resized
   */
  onResize?: (cols: number, rows: number) => void;

  /**
   * Terminal theme (overrides config if provided)
   */
  theme?: "light" | "dark";

  /**
   * Font size in pixels (overrides config if provided)
   */
  fontSize?: number;

  /**
   * Scrollback buffer size in lines (overrides config if provided)
   */
  scrollback?: number;

  /**
   * Mouse tracking mode for enabling mouse event reporting
   * 'none': No mouse tracking (default)
   * 'x10': Send Mouse X & Y on button press
   * 'vt200': Send Mouse X & Y on button press and release
   * 'drag': Use Cell Motion Mouse Tracking
   * 'any': Use All Motion Mouse Tracking
   */
  mouseTracking?: 'none' | 'x10' | 'vt200' | 'drag' | 'any';

  /**
   * Use terminal configuration from localStorage
   * If true, theme/fontSize/scrollback/mouseTracking props are ignored unless explicitly provided
   */
  useConfig?: boolean;
}

export interface XtermTerminalHandle {
  terminal: Terminal | null;
  write: (data: string) => void;
  writeln: (data: string) => void;
  clear: () => void;
  focus: () => void;
  fit: () => void;
  search: (term: string) => boolean;
  searchNext: (term: string) => boolean;
  searchPrevious: (term: string) => boolean;
}

/**
 * XtermTerminal - React wrapper for xterm.js terminal emulator
 *
 * Features:
 * - Canvas-based rendering (10-100x faster than DOM)
 * - WebGL acceleration (2x faster than canvas)
 * - Automatic resizing with FitAddon
 * - Clickable web links
 * - Search functionality
 * - Mouse event reporting (drag-to-select, clicks, etc.)
 * - Professional terminal UX
 */
export const XtermTerminal = forwardRef<XtermTerminalHandle, XtermTerminalProps>(({
  onData,
  onResize,
  theme: themeProp,
  fontSize: fontSizeProp,
  scrollback: scrollbackProp,
  mouseTracking: mouseTrackingProp,
  useConfig = false,
}, ref) => {
  // Load configuration
  const config = useConfig ? loadTerminalConfig() : null;

  // Use props or config values
  const theme = themeProp ?? config?.theme ?? "dark";
  const fontSize = fontSizeProp ?? config?.fontSize ?? 14;
  const scrollback = scrollbackProp ?? config?.scrollbackLines ?? 0;
  const mouseTracking = mouseTrackingProp ?? config?.mouseTracking ?? 'none';
  const fontFamily = config?.fontFamily ?? 'Menlo, Monaco, "Courier New", monospace';
  const cursorStyle = config?.cursorStyle ?? "block";
  const cursorBlink = config?.cursorBlink ?? true;

  const terminalRef = useRef<Terminal | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const searchAddonRef = useRef<SearchAddon | null>(null);
  const webglAddonRef = useRef<WebglAddon | null>(null);
  const lastSizeRef = useRef<{ cols: number; rows: number } | null>(null);

  // Store callbacks in refs to avoid recreating terminal on callback changes
  const onDataRef = useRef(onData);
  const onResizeRef = useRef(onResize);

  useEffect(() => {
    onDataRef.current = onData;
    onResizeRef.current = onResize;
  }, [onData, onResize]);

  // Initialize terminal on mount
  useEffect(() => {
    // SSR guard
    if (typeof window === 'undefined') {
      console.warn('[XtermTerminal] SSR detected, terminal requires client-side rendering');
      return;
    }

    if (!containerRef.current || terminalRef.current) return;

    // Create terminal instance with configuration
    const terminal = new Terminal({
      cursorBlink,
      cursorStyle,
      fontSize,
      fontFamily,
      theme: getTheme(theme),
      scrollback,
      allowProposedApi: true, // Required for some addons
      rightClickSelectsWord: true, // Right-click selects the word under cursor
      mouseTracking // Enable mouse event reporting (proposed API)
    } as any);

    // WebGL mismatch tracker + one-directional Canvas fallback (AC5). See
    // project_plans/terminal-resize-fit-loop/decisions/ADR-001-add-xterm-addon-canvas-dependency.md
    let webglMismatchCount = 0;
    let webglFallbackTriggered = false;

    const triggerCanvasFallback = () => {
      if (webglFallbackTriggered) return; // one-directional latch, never re-arms (pitfalls §4)
      webglFallbackTriggered = true;
      console.warn('[XtermTerminal] WebGL cell-measurement mismatch exceeded threshold, falling back to canvas renderer');

      // @xterm/addon-webgl resolved to 0.18.0 (confirmed in package-lock.json /
      // node_modules). This postdates the historical WebglAddon.dispose()
      // no-op bug (xterm.js #2254, fixed via #2548, a 2019-era fix long since
      // released). The GPU-memory-leak-on-dispose fix (#3889, fixed via
      // #3890) is also merged upstream, but a lightweight web search could not
      // definitively pin the exact release/version boundary where #3890
      // landed relative to 0.18.0 — noting that explicitly rather than
      // asserting an unverified claim (Task 3.0.2).
      webglAddonRef.current?.dispose();
      webglAddonRef.current = null;

      try {
        terminal.loadAddon(new CanvasAddon());
        // Wait one RAF frame after the addon swap before fitting, per the
        // historical xterm.js #1416 crash precedent (measuring against a
        // not-yet-initialized renderer).
        requestAnimationFrame(() => {
          const proposed = fitAddonRef.current?.proposeDimensions();
          if (fitAddonRef.current && isFiniteResizeDimensions(proposed)) {
            fitAddonRef.current.fit();
          } else {
            console.warn('[XtermTerminal] Skipped post-fallback fit: proposed dimensions not finite');
          }
        });
      } catch (err) {
        // adversarial-review.md Blocker: CanvasAddon construction must be
        // guarded, mirroring the WebglAddon try/catch above. If this also
        // throws, the latch stays tripped (no retry) and xterm.js's built-in
        // DOM renderer is left active automatically — no explicit fallback
        // code path is needed (confirmed by build-vs-buy.md research).
        console.error("[XtermTerminal] Canvas renderer also failed to load; falling back to xterm's built-in DOM renderer", err);
      }
    };

    // Dev-only manual trigger (Task 3.2.1a): lets a human visually confirm
    // the Canvas tier renders correctly without waiting for the mismatch
    // heuristic or a real WebGL context loss (jsdom cannot exercise either
    // path for real, so this is otherwise unverifiable pre-ship).
    if (typeof window !== "undefined" && localStorage.getItem("debug-terminal") === "true") {
      (window as any).__staplerSquadForceCanvasFallback = () => triggerCanvasFallback();
    }

    // Create and load addons
    const fitAddon = new FitAddon();
    const webLinksAddon = new WebLinksAddon();
    const searchAddon = new SearchAddon();

    terminal.loadAddon(fitAddon);
    terminal.loadAddon(webLinksAddon);
    terminal.loadAddon(searchAddon);

    // Always enable WebGL renderer for best performance (falls back to canvas if unavailable)
    try {
      webglAddonRef.current = new WebglAddon();
      terminal.loadAddon(webglAddonRef.current);
      webglAddonRef.current.onContextLoss(() => {
        console.warn('[XtermTerminal] WebGL context lost, falling back to canvas renderer');
        triggerCanvasFallback();
      });
      console.log("[XtermTerminal] WebGL renderer enabled");
    } catch (e) {
      console.warn("[XtermTerminal] WebGL not available, using canvas fallback:", e);
    }

    // Open terminal in container with error boundary
    try {
      terminal.open(containerRef.current);

      // CRITICAL: Wait for browser to complete layout before fitting
      // Use requestAnimationFrame to ensure DOM is rendered and measurements are accurate
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          // Double RAF ensures layout is stable before FitAddon measures dimensions
          const containerEl = containerRef.current;
          if (containerEl) {
            const rect = containerEl.getBoundingClientRect();
            console.log(`[XtermTerminal] Container size before fit: ${rect.width}px × ${rect.height}px`);
          }

          // Log what FitAddon will see
          const proposedDims = fitAddon.proposeDimensions();
          console.log(`[XtermTerminal] Proposed dimensions:`, proposedDims);

          // Check if cell dimensions are available (via private API for debugging)
          const dims = (terminal as any)._core?._renderService?.dimensions;
          if (dims?.css?.cell) {
            console.log(`[XtermTerminal] Cell dimensions: ${dims.css.cell.width}px × ${dims.css.cell.height}px`);
          } else {
            console.warn(`[XtermTerminal] Cell dimensions not available yet!`);
          }

          fitAddon.fit();

          console.log(`[XtermTerminal] Initial fit complete: ${terminal.cols} cols × ${terminal.rows} rows`);

          // Calculate actual pixels per column for verification (AC5 mismatch tracker)
          if (containerEl) {
            const mismatchInputs = extractCellMismatchInputs(terminal, containerEl);
            if (mismatchInputs) {
              console.log(`[XtermTerminal] Actual pixels per column: ${mismatchInputs.actualPxPerCol.toFixed(2)}px`);
              console.log(`[XtermTerminal] Expected pixels per column: ${mismatchInputs.expectedPxPerCol.toFixed(2)}px`);
              if (isSustainedMismatch(mismatchInputs.actualPxPerCol, mismatchInputs.expectedPxPerCol, MISMATCH_TOLERANCE_PX)) {
                console.error(`[XtermTerminal] ⚠️ SIZING MISMATCH! Container width doesn't match cell width calculation`);
                if (!webglFallbackTriggered) {
                  webglMismatchCount++;
                  if (webglMismatchCount >= MISMATCH_THRESHOLD) {
                    triggerCanvasFallback();
                  }
                }
              }
            }
          }

          // Force one more fit after a short delay to ensure accurate sizing
          setTimeout(() => {
            const secondProposed = fitAddon.proposeDimensions();
            console.log(`[XtermTerminal] Secondary proposed dimensions:`, secondProposed);
            fitAddon.fit();
            console.log(`[XtermTerminal] Secondary fit complete: ${terminal.cols} cols × ${terminal.rows} rows`);
          }, 100);
        });
      });
    } catch (error) {
      console.error('[XtermTerminal] Terminal initialization failed:', error);
      // Notify parent via resize callback with error indicator (0x0 dimensions)
      if (onResizeRef.current) {
        // Signal error by passing 0x0 dimensions
        // Parent can detect this and show error message
        console.error('[XtermTerminal] Notifying parent of initialization failure');
      }
      return; // Stop initialization
    }

    // Setup event handlers using refs to avoid recreating terminal
    const dataDisposable = terminal.onData((data) => {
      onDataRef.current?.(data);
    });

    // Auto-copy selected text to clipboard on selection change (copyOnSelect behavior)
    const selectionDisposable = terminal.onSelectionChange(() => {
      const selection = terminal.getSelection();
      if (selection) {
        navigator.clipboard.writeText(selection).catch(() => {});
      }
    });

    const resizeDisposable = terminal.onResize(({ cols, rows }) => {
      // Only trigger callback if size actually changed
      const lastSize = lastSizeRef.current;
      if (!lastSize || lastSize.cols !== cols || lastSize.rows !== rows) {
        lastSizeRef.current = { cols, rows };
        onResizeRef.current?.(cols, rows);
      }
    });

    // CRITICAL: Store refs BEFORE triggering callbacks
    // This ensures terminalRef is available when parent component calls getTerminal()
    terminalRef.current = terminal;
    fitAddonRef.current = fitAddon;
    searchAddonRef.current = searchAddon;

    // Now trigger initial resize callback (ref is ready for parent's getTerminal())
    lastSizeRef.current = { cols: terminal.cols, rows: terminal.rows };
    if (onResizeRef.current) {
      onResizeRef.current(terminal.cols, terminal.rows);
    }

    // Setup ResizeObserver for automatic fitting
    // Track container size to avoid unnecessary fit() calls
    let lastContainerSize = { width: 0, height: 0 };
    let resizeCount = 0;
    let resizeTimeout: NodeJS.Timeout | null = null;

    // Decoupled resize sampler (ADR-002): a fixed-cadence re-sampling loop,
    // started by the adaptive debounce below but never reset by further
    // ResizeObserver deliveries once running. See ADR-002 for the full
    // algorithm and rationale.
    let samplerActive = false;
    let sampleTimeout: NodeJS.Timeout | null = null;
    let sampleCount = 0;
    let pendingProposedDims: ResizeDimensions | null = null;

    const stopSampler = () => {
      samplerActive = false;
      pendingProposedDims = null;
      sampleCount = 0;
      if (sampleTimeout) {
        clearTimeout(sampleTimeout);
        sampleTimeout = null;
      }
    };

    const sampleTick = () => {
      if (!fitAddonRef.current || !terminalRef.current) {
        stopSampler();
        return;
      }

      const proposed = fitAddonRef.current.proposeDimensions();
      const applied: ResizeDimensions = {
        cols: terminalRef.current.cols,
        rows: terminalRef.current.rows,
      };
      const result = shouldScheduleFit(proposed, applied, pendingProposedDims);

      if (result.schedule) {
        fitAddonRef.current.fit();
        console.log(`[XtermTerminal] Sampler confirmed resize, fit applied: ${terminalRef.current.cols} cols × ${terminalRef.current.rows} rows`);

        // AC5: accumulate mismatch across confirmed resize events, not just
        // a single startup check (architecture research point 2).
        if (containerRef.current && !webglFallbackTriggered) {
          const mismatchInputs = extractCellMismatchInputs(terminalRef.current, containerRef.current);
          if (
            mismatchInputs &&
            isSustainedMismatch(mismatchInputs.actualPxPerCol, mismatchInputs.expectedPxPerCol, MISMATCH_TOLERANCE_PX)
          ) {
            webglMismatchCount++;
            if (webglMismatchCount >= MISMATCH_THRESHOLD) {
              triggerCanvasFallback();
            }
          }
        }

        stopSampler();
        return;
      }

      if (result.nextPending === null) {
        // At rest (proposed equals applied) or proposeDimensions() returned undefined.
        stopSampler();
        return;
      }

      pendingProposedDims = result.nextPending;
      sampleCount++;

      if (sampleCount >= MAX_SAMPLES) {
        console.warn('[XtermTerminal] Resize did not converge after 20 samples; giving up');
        // Full reset (not a partial abandon): give-up must not leave the
        // sampler permanently inert, since startSamplerIfNeeded() is a
        // no-op whenever samplerActive is already true. See ADR-002.
        stopSampler();
        return;
      }

      sampleTimeout = setTimeout(sampleTick, SAMPLE_INTERVAL_MS);
    };

    const startSamplerIfNeeded = () => {
      if (samplerActive) return;
      samplerActive = true;
      sampleCount = 0;
      pendingProposedDims = null;
      sampleTick();
    };

    const resizeObserver = new ResizeObserver((entries: ResizeObserverEntry[]) => {
      if (!fitAddonRef.current || !terminalRef.current) return;

      const entry = entries[0];
      if (!entry) return;

      // Get current container size
      const { width, height } = entry.contentRect;

      // Only fit if size actually changed (avoid sub-pixel changes)
      const widthChanged = Math.abs(width - lastContainerSize.width) > 1;
      const heightChanged = Math.abs(height - lastContainerSize.height) > 1;

      if (widthChanged || heightChanged) {
        lastContainerSize = { width, height };
        resizeCount++;

        console.log(`[XtermTerminal] Container resized to ${width}px × ${height}px (before fit)`);
        console.log(`[XtermTerminal] Terminal dimensions BEFORE fit: ${terminalRef.current.cols} cols × ${terminalRef.current.rows} rows`);

        // Use minimal debounce for initial resizes (first 3), then increase for stability
        // This ensures ultra-fast initial sizing (10ms) when modal opens, then reduces resize frequency
        const debounceDelay = resizeCount <= 3 ? 10 : 250;

        // Clear any pending resize timeout
        if (resizeTimeout) {
          clearTimeout(resizeTimeout);
        }

        // Schedule sampler start with adaptive debounce. The debounce only
        // decides *when to start* the sampler for a burst of RO deliveries;
        // once started, the sampler's own tick chain is never reset by
        // further RO deliveries (ADR-002).
        resizeTimeout = setTimeout(() => {
          startSamplerIfNeeded();
          resizeTimeout = null;
        }, debounceDelay);
      }
    });

    resizeObserver.observe(containerRef.current);

    // Cleanup
    return () => {
      if (resizeTimeout) {
        clearTimeout(resizeTimeout);
      }
      stopSampler();
      resizeObserver.disconnect();
      dataDisposable.dispose();
      selectionDisposable.dispose();
      resizeDisposable.dispose();
      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
      searchAddonRef.current = null;
      webglAddonRef.current = null;
      if (typeof window !== "undefined") {
        delete (window as any).__staplerSquadForceCanvasFallback;
      }
    };
    // Only recreate terminal if scrollback changes (requires full recreation)
    // Other options can be updated dynamically below
  }, [scrollback]); // Reduced dependencies - only recreate when necessary

  // Update theme dynamically (no terminal recreation needed)
  useEffect(() => {
    if (terminalRef.current) {
      terminalRef.current.options.theme = getTheme(theme);
      terminalRef.current.refresh(0, terminalRef.current.rows - 1);
    }
  }, [theme]);

  // Detect system color scheme changes and update terminal theme accordingly
  // This provides automatic theme switching when no explicit theme prop is given
  useEffect(() => {
    if (typeof window === "undefined" || themeProp !== undefined) return;

    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
    const handleChange = (e: MediaQueryListEvent) => {
      const newTheme = e.matches ? "dark" : "light";
      if (terminalRef.current) {
        terminalRef.current.options.theme = getTheme(newTheme);
        terminalRef.current.refresh(0, terminalRef.current.rows - 1);
      }
    };

    mediaQuery.addEventListener("change", handleChange);
    return () => mediaQuery.removeEventListener("change", handleChange);
  }, [themeProp]);

  // Update font size dynamically (no terminal recreation needed)
  useEffect(() => {
    if (terminalRef.current && terminalRef.current.options.fontSize !== fontSize) {
      terminalRef.current.options.fontSize = fontSize;
      // Defer fit to avoid synchronous resize events
      setTimeout(() => fitAddonRef.current?.fit(), 0);
    }
  }, [fontSize]);

  // Update font family dynamically (no terminal recreation needed)
  useEffect(() => {
    if (terminalRef.current && terminalRef.current.options.fontFamily !== fontFamily) {
      terminalRef.current.options.fontFamily = fontFamily;
      // Defer fit to avoid synchronous resize events
      setTimeout(() => fitAddonRef.current?.fit(), 0);
    }
  }, [fontFamily]);

  // Update cursor options dynamically (no terminal recreation needed)
  useEffect(() => {
    if (terminalRef.current) {
      terminalRef.current.options.cursorStyle = cursorStyle;
      terminalRef.current.options.cursorBlink = cursorBlink;
    }
  }, [cursorStyle, cursorBlink]);

  // Expose terminal methods via ref
  // CRITICAL: Use getter for terminal property to return current ref value
  useImperativeHandle(ref, () => ({
    get terminal() {
      return terminalRef.current;
    },
    write: (data: string) => {
      terminalRef.current?.write(data);
    },
    writeln: (data: string) => {
      terminalRef.current?.writeln(data);
    },
    clear: () => {
      terminalRef.current?.clear();
    },
    focus: () => {
      terminalRef.current?.focus();
    },
    fit: () => {
      fitAddonRef.current?.fit();
    },
    search: (term: string): boolean => {
      if (!searchAddonRef.current) return false;
      return searchAddonRef.current.findNext(term);
    },
    searchNext: (term: string): boolean => {
      if (!searchAddonRef.current) return false;
      return searchAddonRef.current.findNext(term);
    },
    searchPrevious: (term: string): boolean => {
      if (!searchAddonRef.current) return false;
      return searchAddonRef.current.findPrevious(term);
    },
  }), []);

  return (
    <div className={styles.container}>
      <div ref={containerRef} className={styles.terminal} />
    </div>
  );
});

XtermTerminal.displayName = "XtermTerminal";

/**
 * Get xterm.js theme configuration using named theme exports
 */
function getTheme(theme: "light" | "dark") {
  return theme === "light" ? lightTerminalTheme : darkTerminalTheme;
}

/**
 * Debounce helper for resize events
 */
function debounce<T extends (...args: any[]) => void>(
  func: T,
  wait: number
): (...args: Parameters<T>) => void {
  let timeout: NodeJS.Timeout | null = null;

  return function executedFunction(...args: Parameters<T>) {
    const later = () => {
      timeout = null;
      func(...args);
    };

    if (timeout) {
      clearTimeout(timeout);
    }
    timeout = setTimeout(later, wait);
  };
}
