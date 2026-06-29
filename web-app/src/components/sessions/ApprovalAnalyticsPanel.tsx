"use client";

import React, { useState, useCallback } from "react";
import { buildPrefillHref } from "@/lib/ruleBuilderPrefill";
import { useApprovalAnalytics } from "@/lib/hooks/useApprovalAnalytics";
import { useApprovalRules } from "@/lib/hooks/useApprovalRules";
import { DailyBucketProto, SubcommandStatProto, AutoDecision } from "@/gen/session/v1/types_pb";
import { ProgramDetailPanel } from "./ProgramDetailPanel";
import {
  panel, titleRow, title, refreshButton,
  windowSelector, windowBtn, windowBtnActive,
  error as errorClass, retryButton,
  cards, card, cardAllow, cardDeny, cardManual, cardValue, cardLabel, cardSub,
  loading as loadingClass, empty, emptyHint,
  sectionTitle, tableSection, tableWrapper, table, th, thRight, td, tdRight, tdBar, row,
  allowCount, denyCount, manualCount, pctLabel, toolName, ruleName,
  barTrack, barFill, barTool, barRule, barCmd, barPython, barGap,
  categoryBadge, subSectionTitle, filterInput, addRuleLink,
  twoColGrid, twoColCell,
  stackedBarTrack, stackedAllow, stackedDeny, stackedManual,
  gapBadgeHigh, gapBadgeMed, gapBadgeLow, gapBadgeDesc,
  checkboxTh, checkboxTd,
  bulkActionBar, bulkActionCount, bulkAddBtn, bulkClearBtn,
  bulkReviewPanel, bulkReviewHeader, bulkReviewActions, bulkSaveBtn, bulkDiscardBtn,
  bulkResultMsg, decisionSelect, removeEntryBtn,
} from "./ApprovalAnalyticsPanel.css";

// ── types ─────────────────────────────────────────────────────────────────────

type BulkEntry = {
  key: string;
  program: string;
  subcommand: string;
  decision: AutoDecision;
};

type BulkUpsertFn = (rules: Array<{ id: string; name: string; programs: string[]; subcommands: string[]; decision: AutoDecision }>) => Promise<{ created: number; updated: number; errors: string[] }>;

// ── helpers ───────────────────────────────────────────────────────────────────

function pct(count: number, total: number): number {
  if (total === 0) return 0;
  return Math.round((count / total) * 100);
}

function formatDate(iso: string): string {
  // "2006-01-02" → "Jan 15"
  try {
    const d = new Date(iso + "T00:00:00");
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  } catch {
    return iso;
  }
}

// Simple inline bar component — no charting library required.
function Bar({ value, max, className }: { value: number; max: number; className: string }) {
  const width = max === 0 ? 0 : Math.round((value / max) * 100);
  return (
    <div className={barTrack} aria-hidden="true">
      <div className={`${barFill} ${className}`} style={{ width: `${width}%` }} />
    </div>
  );
}

// Stacked bar showing allow/deny/manual composition scaled to max total.
function StackedBar({ allow, deny, manual, total }: { allow: number; deny: number; manual: number; total: number }) {
  const rowTotal = allow + deny + manual;
  const scale = total === 0 ? 0 : rowTotal / total;
  const ap = Math.round(scale * (allow / (rowTotal || 1)) * 100);
  const dp = Math.round(scale * (deny  / (rowTotal || 1)) * 100);
  const mp = Math.round(scale * (manual / (rowTotal || 1)) * 100);
  return (
    <div className={stackedBarTrack} aria-hidden="true">
      {ap > 0 && <div className={stackedAllow} style={{ width: `${ap}%` }} />}
      {dp > 0 && <div className={stackedDeny}  style={{ width: `${dp}%` }} />}
      {mp > 0 && <div className={stackedManual} style={{ width: `${mp}%` }} />}
    </div>
  );
}

// ── component ─────────────────────────────────────────────────────────────────

const WINDOW_OPTIONS = [
  { label: "7 days",  value: 7  },
  { label: "14 days", value: 14 },
  { label: "30 days", value: 30 },
  { label: "90 days", value: 90 },
];

/**
 * ApprovalAnalyticsPanel displays time-series and aggregate data for
 * auto-approval classification decisions.
 *
 * Shows:
 * - Window selector (7 / 14 / 30 / 90 days)
 * - Summary cards: total, auto-allow rate, manual review rate, avg/day
 * - Day-by-day breakdown table with inline bar charts
 * - Top tools and top triggered rules
 */
export function ApprovalAnalyticsPanel() {
  const [windowDays, setWindowDays] = useState(7);
  const [selectedProgram, setSelectedProgram] = useState<string | null>(null);
  const { summary, dailyBuckets, loading, error, refresh } = useApprovalAnalytics({ windowDays });
  const { bulkUpsertRules } = useApprovalRules();

  const total = summary?.totalDecisions ?? 0;
  const autoAllowCount = summary?.decisionCounts["auto_allow"] ?? 0;
  const autoDenyCount  = summary?.decisionCounts["auto_deny"]  ?? 0;
  const escalateCount  = (summary?.decisionCounts["escalate"] ?? 0)
                       + (summary?.decisionCounts["manual_allow"] ?? 0)
                       + (summary?.decisionCounts["manual_deny"] ?? 0);

  const autoAllowRate = pct(autoAllowCount, total);
  const autoDenyRate  = pct(autoDenyCount, total);
  const manualRate    = pct(escalateCount, total);
  const avgPerDay     = dailyBuckets.length > 0 ? Math.round(total / windowDays) : 0;
  const manualAllow   = summary?.decisionCounts["manual_allow"] ?? 0;
  const manualDeny    = summary?.decisionCounts["manual_deny"]  ?? 0;
  const manualTotal   = manualAllow + manualDeny;
  const manualAllowPct = manualTotal > 0 ? Math.round((manualAllow / manualTotal) * 100) : null;

  // Max total across days — used to scale inline bars.
  const maxDayTotal = dailyBuckets.reduce((m, b) => Math.max(m, b.total), 0);

  return (
    <div className={panel}>
      {/* ── Header + window selector ── */}
      <div className={titleRow}>
        <h2 className={title}>Approval Analytics</h2>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <div className={windowSelector} role="group" aria-label="Time window">
            {WINDOW_OPTIONS.map((opt) => (
              <button
                key={opt.value}
                className={`${windowBtn} ${windowDays === opt.value ? windowBtnActive : ""}`}
                onClick={() => setWindowDays(opt.value)}
                aria-pressed={windowDays === opt.value}
              >
                {opt.label}
              </button>
            ))}
          </div>
          <button onClick={refresh} className={refreshButton} disabled={loading} aria-label="Refresh analytics">
            {loading ? "⟳" : "↻"}
          </button>
        </div>
      </div>

      {error && (
        <div className={errorClass} role="alert">
          Failed to load analytics: {error.message}
          <button onClick={refresh} className={retryButton}>Retry</button>
        </div>
      )}

      {/* ── Summary cards ── */}
      <div className={cards}>
        <div className={card}>
          <span className={cardValue}>{total}</span>
          <span className={cardLabel}>Total decisions</span>
          <span className={cardSub}>{avgPerDay}/day avg</span>
        </div>
        <div className={`${card} ${cardAllow}`}>
          <span className={cardValue}>{autoAllowRate}%</span>
          <span className={cardLabel}>Auto-allowed</span>
          <span className={cardSub}>{autoAllowCount} requests</span>
        </div>
        <div className={`${card} ${cardDeny}`}>
          <span className={cardValue}>{autoDenyRate}%</span>
          <span className={cardLabel}>Auto-denied</span>
          <span className={cardSub}>{autoDenyCount} requests</span>
        </div>
        <div className={`${card} ${cardManual}`}>
          <span className={cardValue}>{manualRate}%</span>
          <span className={cardLabel}>Manual review</span>
          <span className={cardSub}>
            {escalateCount} requests
            {manualAllowPct !== null && ` · ${manualAllowPct}% allowed`}
          </span>
        </div>
      </div>

      {/* ── Daily breakdown ── */}
      {loading && dailyBuckets.length === 0 ? (
        <div className={loadingClass}>Loading analytics…</div>
      ) : dailyBuckets.length === 0 ? (
        <div className={empty}>
          No data for the last {windowDays} days.
          <br />
          <span className={emptyHint}>Analytics are recorded when Claude Code sends hook requests.</span>
        </div>
      ) : (
        <div className={tableSection}>
          <h3 className={sectionTitle}>Daily Breakdown</h3>
          <div className={tableWrapper}>
            <table className={table}>
              <thead>
                <tr>
                  <th className={th}>Date</th>
                  <th className={`${th} ${thRight}`}>Total</th>
                  <th className={`${th} ${thRight}`}>Allow</th>
                  <th className={`${th} ${thRight}`}>Deny</th>
                  <th className={`${th} ${thRight}`}>Manual</th>
                  <th className={th}>Volume</th>
                </tr>
              </thead>
              <tbody>
                {[...dailyBuckets].reverse().map((b) => {
                  const manualTotal = b.escalate + b.manualAllow + b.manualDeny;
                  return (
                    <tr key={b.date} className={row}>
                      <td className={td}>{formatDate(b.date)}</td>
                      <td className={`${td} ${tdRight}`}>{b.total}</td>
                      <td className={`${td} ${tdRight}`}>
                        <span className={allowCount}>{b.autoAllow}</span>
                        <span className={pctLabel}> {pct(b.autoAllow, b.total)}%</span>
                      </td>
                      <td className={`${td} ${tdRight}`}>
                        <span className={denyCount}>{b.autoDeny}</span>
                        <span className={pctLabel}> {pct(b.autoDeny, b.total)}%</span>
                      </td>
                      <td className={`${td} ${tdRight}`}>
                        <span className={manualCount}>{manualTotal}</span>
                        <span className={pctLabel}> {pct(manualTotal, b.total)}%</span>
                      </td>
                      <td className={`${td} ${tdBar}`}>
                        <StackedBar
                          allow={b.autoAllow}
                          deny={b.autoDeny}
                          manual={manualTotal}
                          total={maxDayTotal}
                        />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* ── Top tools + Top triggered rules (side-by-side) ── */}
      {summary && (summary.topTools.length > 0 || summary.topTriggeredRules.length > 0) && (
        <div className={twoColGrid}>
          {summary.topTools.length > 0 && (
            <div className={`${tableSection} ${twoColCell}`}>
              <h3 className={sectionTitle}>Top Tools</h3>
              <div className={tableWrapper}>
                <table className={table}>
                  <thead>
                    <tr>
                      <th className={th}>Tool</th>
                      <th className={`${th} ${thRight}`}>Requests</th>
                      <th className={th}>Share</th>
                    </tr>
                  </thead>
                  <tbody>
                    {summary.topTools.map((t) => (
                      <tr key={t.toolName} className={row}>
                        <td className={td}><code className={toolName}>{t.toolName}</code></td>
                        <td className={`${td} ${tdRight}`}>{t.count}</td>
                        <td className={`${td} ${tdBar}`}>
                          <Bar value={t.count} max={summary.topTools[0]?.count ?? 1} className={barTool} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
          {summary.topTriggeredRules.length > 0 && (
            <div className={`${tableSection} ${twoColCell}`}>
              <h3 className={sectionTitle}>Top Triggered Rules</h3>
              <div className={tableWrapper}>
                <table className={table}>
                  <thead>
                    <tr>
                      <th className={th}>Rule</th>
                      <th className={`${th} ${thRight}`}>Triggers</th>
                      <th className={th}>Frequency</th>
                    </tr>
                  </thead>
                  <tbody>
                    {summary.topTriggeredRules.map((r) => (
                      <tr key={r.ruleId} className={row}>
                        <td className={td}><span className={ruleName}>{r.ruleName || r.ruleId}</span></td>
                        <td className={`${td} ${tdRight}`}>{r.count}</td>
                        <td className={`${td} ${tdBar}`}>
                          <Bar value={r.count} max={summary.topTriggeredRules[0]?.count ?? 1} className={barRule} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </div>
      )}

      {/* ── Top Python imports ── */}
      {summary && summary.topPythonImports.length > 0 && (
        <div className={tableSection}>
          <h3 className={sectionTitle}>Top Python Imports</h3>
          <div className={tableWrapper}>
            <table className={table}>
              <thead>
                <tr>
                  <th className={th}>Module</th>
                  <th className={`${th} ${thRight}`}>Uses</th>
                  <th className={th}>Share</th>
                </tr>
              </thead>
              <tbody>
                {summary.topPythonImports.map((imp) => (
                  <tr key={imp.module} className={row}>
                    <td className={td}><code className={toolName}>{imp.module}</code></td>
                    <td className={`${td} ${tdRight}`}>{imp.count}</td>
                    <td className={`${td} ${tdBar}`}>
                      <Bar value={imp.count} max={summary.topPythonImports[0]?.count ?? 1} className={barPython} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* ── Command distribution ── */}
      {summary && summary.commandSubcommandStats.length > 0 && (
        <div className={tableSection}>
          <h3 className={sectionTitle}>Command Distribution</h3>
          <CommandDistributionTable stats={summary.commandSubcommandStats} bulkUpsert={bulkUpsertRules} />
        </div>
      )}

      {/* ── Rule coverage gaps ── */}
      {summary && summary.coverageGapCount > 0 && (
        <div className={tableSection}>
          <div style={{ display: "flex", alignItems: "baseline", gap: 4, marginBottom: 12, flexWrap: "wrap" }}>
            <h3 className={sectionTitle} style={{ margin: 0 }}>Coverage Gaps</h3>
            {(() => {
              const rounded = Math.round(summary.coverageGapRate);
              const cls = rounded >= 30 ? gapBadgeHigh : rounded >= 10 ? gapBadgeMed : gapBadgeLow;
              return <span className={cls}>{rounded}% uncovered</span>;
            })()}
            <span className={gapBadgeDesc}>{summary.coverageGapCount} of {total} decisions had no matching rule</span>
          </div>

          {summary.topUncoveredTools.length > 0 && (
            <>
              <h4 className={subSectionTitle}>Uncovered Tools</h4>
              <UncoveredToolsTable tools={summary.topUncoveredTools} bulkUpsert={bulkUpsertRules} />
            </>
          )}

          {summary.topUncoveredPrograms.length > 0 && (
            <>
              <h4 className={subSectionTitle}>Uncovered Bash Programs</h4>
              <UncoveredProgramsTable
                programs={summary.topUncoveredPrograms}
                windowDays={windowDays}
                selectedProgram={selectedProgram}
                onSelectProgram={setSelectedProgram}
                bulkUpsert={bulkUpsertRules}
              />
            </>
          )}
        </div>
      )}
    </div>
  );
}

// ── BulkReviewPanel ───────────────────────────────────────────────────────────

function BulkReviewPanel({
  entries,
  onEntriesChange,
  bulkUpsert,
  onDone,
}: {
  entries: BulkEntry[];
  onEntriesChange: (entries: BulkEntry[]) => void;
  bulkUpsert: BulkUpsertFn;
  onDone: () => void;
}) {
  const [saving, setSaving] = useState(false);
  const [result, setResult] = useState("");

  const setDecision = useCallback((key: string, decision: AutoDecision) => {
    onEntriesChange(entries.map((e) => (e.key === key ? { ...e, decision } : e)));
  }, [entries, onEntriesChange]);

  const removeEntry = useCallback((key: string) => {
    const next = entries.filter((e) => e.key !== key);
    if (next.length === 0) { onDone(); return; }
    onEntriesChange(next);
  }, [entries, onEntriesChange, onDone]);

  const handleSave = useCallback(async () => {
    setSaving(true);
    setResult("");
    try {
      const rules = entries.map((e) => ({
        id: "",
        name: e.subcommand ? `${e.program} ${e.subcommand}` : e.program,
        programs: [e.program],
        subcommands: e.subcommand ? [e.subcommand] : [],
        decision: e.decision,
      }));
      const resp = await bulkUpsert(rules);
      setResult(`✓ Created ${resp.created}, updated ${resp.updated}`);
    } catch (err) {
      setResult(`Error: ${err instanceof Error ? err.message : "unknown error"}`);
    } finally {
      setSaving(false);
    }
  }, [entries, bulkUpsert, onDone]);

  return (
    <div className={bulkReviewPanel}>
      <div className={bulkReviewHeader}>
        <span>Review {entries.length} new rule{entries.length !== 1 ? "s" : ""}</span>
        <div className={bulkReviewActions}>
          {result && <span className={bulkResultMsg} role="status">{result}</span>}
          {result ? (
            <button className={bulkSaveBtn} onClick={onDone}>Done</button>
          ) : (
            <button className={bulkSaveBtn} onClick={handleSave} disabled={saving}>
              {saving ? "Saving…" : "Save all"}
            </button>
          )}
          {!result && <button className={bulkDiscardBtn} onClick={onDone} disabled={saving}>Cancel</button>}
        </div>
      </div>
      <div className={tableWrapper}>
        <table className={table}>
          <thead>
            <tr>
              <th className={th}>Program</th>
              <th className={th}>Subcommand</th>
              <th className={th}>Decision</th>
              <th className={th}></th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e) => (
              <tr key={e.key} className={row}>
                <td className={td}><code className={toolName}>{e.program}</code></td>
                <td className={td}>{e.subcommand ? <code className={toolName}>{e.subcommand}</code> : <span style={{ color: "var(--text-muted)" }}>any</span>}</td>
                <td className={td}>
                  <select
                    className={decisionSelect}
                    value={e.decision}
                    onChange={(ev) => setDecision(e.key, Number(ev.target.value) as AutoDecision)}
                    aria-label={`Decision for ${e.program} ${e.subcommand}`}
                  >
                    <option value={AutoDecision.ALLOW}>Allow</option>
                    <option value={AutoDecision.DENY}>Deny</option>
                    <option value={AutoDecision.ESCALATE}>Escalate (manual)</option>
                  </select>
                </td>
                <td className={td}>
                  <button className={removeEntryBtn} onClick={() => removeEntry(e.key)} aria-label="Remove">✕</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// ── CommandDistributionTable ───────────────────────────────────────────────────

function CommandDistributionTable({ stats, bulkUpsert }: { stats: SubcommandStatProto[]; bulkUpsert: BulkUpsertFn }) {
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [reviewEntries, setReviewEntries] = useState<BulkEntry[] | null>(null);

  const lc = filter.toLowerCase();
  const filtered = lc
    ? stats.filter(
        (s) =>
          s.programName.toLowerCase().includes(lc) ||
          s.subcommand.toLowerCase().includes(lc)
      )
    : stats;
  const maxCount = filtered[0]?.count ?? 1;

  const allFilteredKeys = filtered.map((s) => `${s.programName}:${s.subcommand}`);
  const allSelected = allFilteredKeys.length > 0 && allFilteredKeys.every((k) => selected.has(k));
  const someSelected = selected.size > 0;

  const toggleAll = () => {
    if (allSelected) {
      setSelected((prev) => {
        const next = new Set(prev);
        allFilteredKeys.forEach((k) => next.delete(k));
        return next;
      });
    } else {
      setSelected((prev) => new Set([...prev, ...allFilteredKeys]));
    }
  };

  const toggleRow = (key: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  };

  const openReview = () => {
    const entries: BulkEntry[] = stats
      .filter((s) => selected.has(`${s.programName}:${s.subcommand}`))
      .map((s) => ({
        key: `${s.programName}:${s.subcommand}`,
        program: s.programName,
        subcommand: s.subcommand,
        decision: AutoDecision.ALLOW,
      }));
    setReviewEntries(entries);
  };

  const closeReview = () => {
    setReviewEntries(null);
    setSelected(new Set());
  };

  return (
    <>
      <input
        type="text"
        placeholder="Filter by program or subcommand (e.g. gh, sed, aws s3)…"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        className={filterInput}
        aria-label="Filter command distribution entries"
      />
      {someSelected && !reviewEntries && (
        <div className={bulkActionBar}>
          <span className={bulkActionCount}>{selected.size} selected</span>
          <button className={bulkAddBtn} onClick={openReview}>Add rules for selected →</button>
          <button className={bulkClearBtn} onClick={() => setSelected(new Set())}>Clear</button>
        </div>
      )}
      {reviewEntries && (
        <BulkReviewPanel
          entries={reviewEntries}
          onEntriesChange={setReviewEntries}
          bulkUpsert={bulkUpsert}
          onDone={closeReview}
        />
      )}
      <div className={tableWrapper}>
        <table className={table}>
          <thead>
            <tr>
              <th className={checkboxTh}>
                <input
                  type="checkbox"
                  checked={allSelected}
                  onChange={toggleAll}
                  aria-label="Select all"
                  disabled={filtered.length === 0}
                />
              </th>
              <th className={th}>Program</th>
              <th className={th}>Subcommand</th>
              <th className={th}>Category</th>
              <th className={`${th} ${thRight}`}>Calls</th>
              <th className={th}>Share</th>
              <th className={th}></th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((s) => {
              const key = `${s.programName}:${s.subcommand}`;
              return (
                <tr key={key} className={row}>
                  <td className={checkboxTd}>
                    <input
                      type="checkbox"
                      checked={selected.has(key)}
                      onChange={() => toggleRow(key)}
                      aria-label={`Select ${s.programName} ${s.subcommand}`}
                    />
                  </td>
                  <td className={td}>
                    <code className={toolName}>{s.programName}</code>
                  </td>
                  <td className={td}>
                    <code className={toolName}>{s.subcommand}</code>
                  </td>
                  <td className={td}>
                    <span className={categoryBadge}>{s.category}</span>
                  </td>
                  <td className={`${td} ${tdRight}`}>{s.count}</td>
                  <td className={`${td} ${tdBar}`}>
                    <Bar value={s.count} max={maxCount} className={barCmd} />
                  </td>
                  <td className={td}>
                    <a
                      href={buildPrefillHref({
                        programs: [s.programName],
                        subcommands: s.subcommand ? [s.subcommand] : [],
                      })}
                      className={addRuleLink}
                      title={`Add a rule for ${s.programName} ${s.subcommand}`}
                    >
                      Add rule →
                    </a>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

// ── UncoveredToolsTable ───────────────────────────────────────────────────────

type UncoveredTool = { toolName: string; count: number };

function UncoveredToolsTable({ tools, bulkUpsert }: { tools: UncoveredTool[]; bulkUpsert: BulkUpsertFn }) {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [reviewEntries, setReviewEntries] = useState<BulkEntry[] | null>(null);

  const allSelected = tools.length > 0 && tools.every((t) => selected.has(t.toolName));

  const toggleAll = () => {
    if (allSelected) setSelected(new Set());
    else setSelected(new Set(tools.map((t) => t.toolName)));
  };

  const toggleRow = (key: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  };

  const openReview = () => {
    const entries: BulkEntry[] = tools
      .filter((t) => selected.has(t.toolName))
      .map((t) => ({ key: t.toolName, program: t.toolName, subcommand: "", decision: AutoDecision.ALLOW }));
    setReviewEntries(entries);
  };

  const closeReview = () => { setReviewEntries(null); setSelected(new Set()); };

  return (
    <>
      {selected.size > 0 && !reviewEntries && (
        <div className={bulkActionBar}>
          <span className={bulkActionCount}>{selected.size} selected</span>
          <button className={bulkAddBtn} onClick={openReview}>Add rules for selected →</button>
          <button className={bulkClearBtn} onClick={() => setSelected(new Set())}>Clear</button>
        </div>
      )}
      {reviewEntries && (
        <BulkReviewPanel entries={reviewEntries} onEntriesChange={setReviewEntries} bulkUpsert={bulkUpsert} onDone={closeReview} />
      )}
      <div className={tableWrapper}>
        <table className={table}>
          <thead>
            <tr>
              <th className={checkboxTh}><input type="checkbox" checked={allSelected} onChange={toggleAll} aria-label="Select all tools" /></th>
              <th className={th}>Tool</th>
              <th className={`${th} ${thRight}`}>Unmatched</th>
              <th className={th}>Share of gaps</th>
              <th className={th}></th>
            </tr>
          </thead>
          <tbody>
            {tools.map((t) => (
              <tr key={t.toolName} className={row}>
                <td className={checkboxTd}><input type="checkbox" checked={selected.has(t.toolName)} onChange={() => toggleRow(t.toolName)} aria-label={`Select ${t.toolName}`} /></td>
                <td className={td}><code className={toolName}>{t.toolName}</code></td>
                <td className={`${td} ${tdRight}`}>{t.count}</td>
                <td className={`${td} ${tdBar}`}>
                  <Bar value={t.count} max={tools[0]?.count ?? 1} className={barGap} />
                </td>
                <td className={td}>
                  <a href={buildPrefillHref({ toolName: t.toolName })} className={addRuleLink} title={`Add a rule for ${t.toolName}`}>
                    Add rule →
                  </a>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

// ── UncoveredProgramsTable ────────────────────────────────────────────────────

type UncoveredProgram = { programName: string; category: string; count: number };

function UncoveredProgramsTable({
  programs, windowDays, selectedProgram, onSelectProgram, bulkUpsert,
}: {
  programs: UncoveredProgram[];
  windowDays: number;
  selectedProgram: string | null;
  onSelectProgram: (p: string | null) => void;
  bulkUpsert: BulkUpsertFn;
}) {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [reviewEntries, setReviewEntries] = useState<BulkEntry[] | null>(null);

  const allSelected = programs.length > 0 && programs.every((p) => selected.has(p.programName));

  const toggleAll = () => {
    if (allSelected) setSelected(new Set());
    else setSelected(new Set(programs.map((p) => p.programName)));
  };

  const toggleRow = (key: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  };

  const openReview = () => {
    const entries: BulkEntry[] = programs
      .filter((p) => selected.has(p.programName))
      .map((p) => ({ key: p.programName, program: p.programName, subcommand: "", decision: AutoDecision.ALLOW }));
    setReviewEntries(entries);
  };

  const closeReview = () => { setReviewEntries(null); setSelected(new Set()); };

  return (
    <>
      {selected.size > 0 && !reviewEntries && (
        <div className={bulkActionBar}>
          <span className={bulkActionCount}>{selected.size} selected</span>
          <button className={bulkAddBtn} onClick={openReview}>Add rules for selected →</button>
          <button className={bulkClearBtn} onClick={() => setSelected(new Set())}>Clear</button>
        </div>
      )}
      {reviewEntries && (
        <BulkReviewPanel entries={reviewEntries} onEntriesChange={setReviewEntries} bulkUpsert={bulkUpsert} onDone={closeReview} />
      )}
      <div className={tableWrapper}>
        <table className={table}>
          <thead>
            <tr>
              <th className={checkboxTh}><input type="checkbox" checked={allSelected} onChange={toggleAll} aria-label="Select all programs" /></th>
              <th className={th}>Program</th>
              <th className={th}>Category</th>
              <th className={`${th} ${thRight}`}>Unmatched</th>
              <th className={th}>Share of gaps</th>
              <th className={th}></th>
            </tr>
          </thead>
          <tbody>
            {programs.map((p) => {
              const isDrillOpen = selectedProgram === p.programName;
              return (
                <React.Fragment key={p.programName}>
                  <tr
                    className={row}
                    style={{ cursor: "pointer" }}
                    tabIndex={0}
                    role="button"
                    aria-expanded={isDrillOpen}
                    aria-label={`${p.programName} — click to ${isDrillOpen ? "collapse" : "expand"} details`}
                    onClick={() => onSelectProgram(isDrillOpen ? null : p.programName)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        onSelectProgram(isDrillOpen ? null : p.programName);
                      }
                    }}
                  >
                    <td className={checkboxTd} onClick={(e) => e.stopPropagation()}>
                      <input type="checkbox" checked={selected.has(p.programName)} onChange={() => toggleRow(p.programName)} aria-label={`Select ${p.programName}`} />
                    </td>
                    <td className={td}><code className={toolName}>{p.programName}</code></td>
                    <td className={td}><span className={categoryBadge}>{p.category}</span></td>
                    <td className={`${td} ${tdRight}`}>{p.count}</td>
                    <td className={`${td} ${tdBar}`}>
                      <Bar value={p.count} max={programs[0]?.count ?? 1} className={barGap} />
                    </td>
                    <td className={td}>
                      <a href={buildPrefillHref({ programs: [p.programName] })} className={addRuleLink} title={`Add a rule for ${p.programName}`} onClick={(e) => e.stopPropagation()}>
                        Add rule →
                      </a>
                    </td>
                  </tr>
                  {isDrillOpen && (
                    <tr>
                      <td colSpan={6} onClick={(e) => e.stopPropagation()}>
                        <ProgramDetailPanel program={p.programName} windowDays={windowDays} onClose={() => onSelectProgram(null)} />
                      </td>
                    </tr>
                  )}
                </React.Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

