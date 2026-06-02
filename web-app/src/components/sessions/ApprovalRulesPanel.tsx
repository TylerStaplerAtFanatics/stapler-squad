"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Modal, ModalContent, ModalTitle, ModalClose } from "@/components/ui/Modal";
import { useApprovalRules } from "@/lib/hooks/useApprovalRules";
import { useApprovalAnalytics } from "@/lib/hooks/useApprovalAnalytics";
import { useGenerateRule } from "@/lib/hooks/useGenerateRule";
import { useExportRules } from "@/lib/hooks/useExportRules";
import { ApprovalRuleProto, AutoDecision, SuggestionSource } from "@/gen/session/v1/types_pb";
import { SuggestedRuleCard } from "./SuggestedRuleCard";
import { ImportRulesModal } from "./ImportRulesModal";
import {
  panel, header, titleRow, title, subtitle, refreshButton,
  analyticsBar, analyticsTotal, analyticsRate, rateAllow, rateManual, analyticsTopTool,
  tabs, tab, tabActive, tabLabelFull, tabLabelShort,
  error as errorClass, retryButton,
  loading as loadingClass, empty,
  tableWrapper, table, th, td, tdCenter, row, rowDisabled,
  ruleName, ruleReason, ruleAlt, matchInfo, matchChip,
  decisionBadge, decisionAllow, decisionDeny, decisionEscalate,
  sourceBadge, toggle, toggleOn, toggleOff, deleteButton, builtInBadge,
  addButton, mobileAddFab, headerButtonsHiddenOnMobile,
  form as formClass, formTitle, formError as formErrorClass,
  formGrid, formSection, formSectionHeader, priorityHint,
  label, input, select,
  formActions, saveButton, cancelButton,
  generateButtonRow, generateButton, cancelGenerateButton,
  generateErrorBanner, dismissErrorButton, suggestionsContainer,
  commandSampleDetails, commandSampleSummary, commandSampleBody,
  commandSampleTextarea, commandSampleActions, aiGeneratedBadge,
  ruleModalContent, modalHeader, modalTitleRow, modalBody, modalCloseButton,
  rowCount,
} from "./ApprovalRulesPanel.css";

// ── helpers ──────────────────────────────────────────────────────────────────

/** Escape all regex metacharacters in a literal string for safe interpolation into patterns. */
function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function decisionLabel(d: AutoDecision): string {
  switch (d) {
    case AutoDecision.ALLOW: return "Auto-Allow";
    case AutoDecision.DENY:  return "Auto-Deny";
    default:                 return "Escalate";
  }
}

function decisionClass(d: AutoDecision): string {
  switch (d) {
    case AutoDecision.ALLOW: return decisionAllow;
    case AutoDecision.DENY:  return decisionDeny;
    default:                 return decisionEscalate;
  }
}

function sourceLabel(s: string): string {
  switch (s) {
    case "user":            return "Custom";
    case "seed":            return "Built-in";
    case "claude-settings": return "Claude Settings";
    default:                return s;
  }
}

// ── empty form state ──────────────────────────────────────────────────────────

interface RuleFormState {
  name: string;
  toolName: string;
  toolPattern: string;
  commandPattern: string;
  filePattern: string;
  criteriaPrograms: string[];
  criteriaSubcommands: string[];
  decision: AutoDecision;
  reason: string;
  alternative: string;
  priority: number;
  enabled: boolean;
}

const emptyForm: RuleFormState = {
  name: "",
  toolName: "",
  toolPattern: "",
  commandPattern: "",
  filePattern: "",
  criteriaPrograms: [],
  criteriaSubcommands: [],
  decision: AutoDecision.ALLOW,
  reason: "",
  alternative: "",
  priority: 10,
  enabled: true,
};

// ── component ─────────────────────────────────────────────────────────────────

/**
 * ApprovalRulesPanel shows the list of auto-approval rules and lets users
 * create, toggle, and delete custom rules.
 *
 * Built-in (seed) and claude-settings rules are shown read-only.
 */
export function ApprovalRulesPanel() {
  const { rules, loading, error, upsertRule, deleteRule, refresh } = useApprovalRules();
  const { summary, loading: analyticsLoading } = useApprovalAnalytics({ windowDays: 7 });
  const { exportRules, loading: exporting, error: exportError } = useExportRules();

  // ── Epic 3: panel-level "Generate Suggestions" hook ─────────────────────
  const {
    suggestions,
    loading: genLoading,
    error: genError,
    generate,
    cancel,
    clear,
  } = useGenerateRule();

  // ── Epic 6: command-sample generate hook (separate instance) ─────────────
  const {
    suggestions: cmdSuggestions,
    loading: cmdGenLoading,
    generate: cmdGenerate,
    clear: cmdGenClear,
  } = useGenerateRule();

  const nameInputRef = useRef<HTMLInputElement>(null);

  const [sourceFilter, setSourceFilter] = useState<string>("all");
  const [showForm, setShowForm] = useState(false);
  const [importModalOpen, setImportModalOpen] = useState(false);
  const [form, setForm] = useState<RuleFormState>(emptyForm);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [aiPrefilled, setAiPrefilled] = useState(false);
  const [cmdSampleValue, setCmdSampleValue] = useState("");

  // Track which form fields the user has manually edited (not overwritten by AI pre-fill).
  const touchedFieldsRef = useRef<Set<keyof RuleFormState>>(new Set());

  // ── URL param pre-fill (from analytics "Add rule →" links) ───────────────
  // Runs once on mount (client only). Reads window.location.search directly to
  // avoid useSearchParams + Suspense complications in the static export.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const tool = params.get("tool");
    const program = params.get("program");
    const subcommand = params.get("subcommand");
    const open = params.get("open");
    if (!tool && !program && !open) return;

    const prefill: Partial<RuleFormState> = {};
    if (tool) {
      prefill.toolName = tool;
      prefill.name = `Allow ${tool}`;
    } else if (program) {
      prefill.toolName = "Bash";
      prefill.criteriaPrograms = [program];
      if (subcommand) {
        prefill.criteriaSubcommands = [subcommand];
        prefill.name = `Allow ${program} ${subcommand}`;
      } else {
        prefill.name = `Allow ${program}`;
      }
    }

    setShowForm(true);
    setForm({ ...emptyForm, ...prefill });
    setFormError(null);
    setAiPrefilled(false);
    setCmdSampleValue("");
    touchedFieldsRef.current = new Set();
    cmdGenClear();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ── filter ────────────────────────────────────────────────────────────────

  const visibleRules = sourceFilter === "all"
    ? rules
    : rules.filter((r) => r.source === sourceFilter);

  // ── save handler ──────────────────────────────────────────────────────────

  const handleSave = async () => {
    if (!form.name.trim()) {
      setFormError("Name is required.");
      return;
    }
    if (!form.toolName && !form.toolPattern && !form.commandPattern && !form.filePattern && form.criteriaPrograms.length === 0) {
      setFormError("At least one of Tool Name, Tool Pattern, Command Pattern, File Pattern, or Programs is required.");
      return;
    }
    setFormError(null);
    setSaving(true);
    try {
      const id = `user-${Date.now()}`;
      await upsertRule({ id, ...form, riskLevel: "", criteriaPrograms: form.criteriaPrograms, criteriaSubcommands: form.criteriaSubcommands });
      setForm(emptyForm);
      setShowForm(false);
    } catch (e) {
      setFormError(e instanceof Error ? e.message : "Failed to save rule.");
    } finally {
      setSaving(false);
    }
  };

  // ── toggle enabled ────────────────────────────────────────────────────────

  const handleToggle = async (rule: ApprovalRuleProto) => {
    if (rule.source !== "user") return;
    try {
      await upsertRule({
        id: rule.id,
        name: rule.name,
        toolName: rule.toolName,
        toolPattern: rule.toolPattern,
        commandPattern: rule.commandPattern,
        filePattern: rule.filePattern,
        criteriaPrograms: rule.criteriaPrograms,
        criteriaSubcommands: rule.criteriaSubcommands,
        decision: rule.decision,
        riskLevel: rule.riskLevel,
        reason: rule.reason,
        alternative: rule.alternative,
        priority: rule.priority,
        enabled: !rule.enabled,
      });
    } catch (e) {
      console.error("Failed to toggle rule:", e);
    }
  };

  // ── open/close form ───────────────────────────────────────────────────────

  const openForm = () => {
    setShowForm(true);
    setForm(emptyForm);
    setFormError(null);
    setAiPrefilled(false);
    setCmdSampleValue("");
    touchedFieldsRef.current = new Set();
    cmdGenClear();
  };

  const closeForm = () => {
    setShowForm(false);
    setForm(emptyForm);
    setFormError(null);
    setAiPrefilled(false);
    setCmdSampleValue("");
    touchedFieldsRef.current = new Set();
    cmdGenClear();
  };

  // ── Epic 6: pre-fill form from command-sample suggestion ──────────────────
  // useEffect ensures prefill runs after the form is open (showForm=true) and
  // only when cmdSuggestions actually changes.
  useEffect(() => {
    if (!showForm) return;
    if (cmdSuggestions.length === 0) return;
    const suggestion = cmdSuggestions[0];
    const touched = touchedFieldsRef.current;
    setForm((prev) => ({
      name:                touched.has("name")                ? prev.name                : suggestion.name || prev.name,
      toolName:            touched.has("toolName")            ? prev.toolName            : suggestion.toolName || prev.toolName,
      toolPattern:         touched.has("toolPattern")         ? prev.toolPattern         : suggestion.toolPattern || prev.toolPattern,
      commandPattern:      touched.has("commandPattern")      ? prev.commandPattern      : suggestion.commandPattern || prev.commandPattern,
      filePattern:         touched.has("filePattern")         ? prev.filePattern         : suggestion.filePattern || prev.filePattern,
      criteriaPrograms:    touched.has("criteriaPrograms")    ? prev.criteriaPrograms    : prev.criteriaPrograms,
      criteriaSubcommands: touched.has("criteriaSubcommands") ? prev.criteriaSubcommands : prev.criteriaSubcommands,
      decision:            touched.has("decision")            ? prev.decision            : (suggestion.decision !== AutoDecision.UNSPECIFIED ? suggestion.decision : prev.decision),
      reason:              touched.has("reason")              ? prev.reason              : suggestion.reason || prev.reason,
      alternative:         touched.has("alternative")         ? prev.alternative         : suggestion.alternative || prev.alternative,
      priority:            touched.has("priority")            ? prev.priority            : (suggestion.priority > 0 ? suggestion.priority : prev.priority),
      enabled:             prev.enabled,
    }));
    setAiPrefilled(true);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cmdSuggestions, showForm]);

  // ── Epic 6: handle manual field changes (mark touched) ───────────────────

  const setFormField = <K extends keyof RuleFormState>(key: K, value: RuleFormState[K]) => {
    touchedFieldsRef.current.add(key);
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  // ── Epic 3: handle suggestion cards ──────────────────────────────────────

  const [dismissedIndices, setDismissedIndices] = useState<Set<number>>(new Set());

  const handleSuggestionAccept = (_savedRule: ApprovalRuleProto) => {
    clear();
    refresh();
    setDismissedIndices(new Set());
  };

  const handleSuggestionDiscard = (idx: number) => {
    setDismissedIndices((prev) => new Set([...prev, idx]));
  };

  const visibleSuggestions = suggestions.filter((_, idx) => !dismissedIndices.has(idx));

  // ── aria-live announcement for generate state changes ─────────────────────

  const genLiveMessage = useMemo(() => {
    if (genLoading) return "Generating rule suggestions…";
    if (suggestions.length > 0) return "Rule suggestions ready.";
    return "";
  }, [genLoading, suggestions.length]);

  // ── analytics summary bar ─────────────────────────────────────────────────

  const autoAllowRate = summary ? Math.round(summary.autoApproveRate * 100) : null;
  const manualRate    = summary ? Math.round(summary.manualReviewRate * 100) : null;
  const total         = summary ? summary.totalDecisions : null;

  // ── render ────────────────────────────────────────────────────────────────

  return (
    <div className={panel}>
      {/* ── Header ── */}
      <div className={header}>
        <div className={titleRow}>
          <h2 className={title}>Approval Rules</h2>
          <div className={generateButtonRow}>
            {/* Epic 3: Generate Suggestions button — hidden on mobile (low priority action) */}
            <button
              onClick={() => {
                setDismissedIndices(new Set());
                void generate({ source: SuggestionSource.ANALYTICS_GAPS });
              }}
              className={`${generateButton} ${headerButtonsHiddenOnMobile}`}
              disabled={genLoading}
              data-testid="generate-suggestions"
            >
              {genLoading ? "Generating…" : "Generate Suggestions"}
            </button>
            {genLoading && (
              <button
                onClick={cancel}
                className={`${cancelGenerateButton} ${headerButtonsHiddenOnMobile}`}
                data-testid="cancel-generate-button"
              >
                Cancel
              </button>
            )}
            {/* Export YAML button — hidden on mobile (consistent with other header buttons) */}
            <button
              className={`${addButton} ${headerButtonsHiddenOnMobile}`}
              onClick={() => void exportRules()}
              disabled={exporting}
              data-testid="export-yaml-button"
            >
              {exporting ? "Exporting…" : "Export YAML"}
            </button>
            {/* Import YAML button */}
            <button
              className={`${addButton} ${headerButtonsHiddenOnMobile}`}
              onClick={() => setImportModalOpen(true)}
              data-testid="import-yaml-button"
            >
              Import YAML
            </button>
            <button className={`${addButton} ${headerButtonsHiddenOnMobile}`} onClick={openForm} data-testid="add-rule-button">
              + Add Rule
            </button>
            <button
              onClick={refresh}
              className={refreshButton}
              disabled={loading}
              aria-label="Refresh rules"
              title="Refresh rules"
            >
              {loading ? "⟳" : "↻"}
            </button>
          </div>
        </div>
        <p className={subtitle}>
          Rules are evaluated in priority order before requests reach the manual review queue.
        </p>
      </div>

      {/* ── Accessible live region for generation state ── */}
      <span
        aria-live="polite"
        aria-atomic="true"
        style={{ position: "absolute", width: 1, height: 1, padding: 0, overflow: "hidden", clip: "rect(0,0,0,0)", whiteSpace: "nowrap", border: 0 }}
      >
        {genLiveMessage}
      </span>

      {/* ── Generate error banner ── */}
      {genError && (
        <div className={generateErrorBanner} role="alert" data-testid="generate-error-banner">
          <span>Failed to generate suggestions: {genError.message}</span>
          <button
            className={dismissErrorButton}
            onClick={clear}
            aria-label="Dismiss error"
            data-testid="dismiss-error-button"
          >
            ×
          </button>
        </div>
      )}

      {/* ── Export error banner ── */}
      {exportError && (
        <div className={generateErrorBanner} role="alert" data-testid="export-error-banner">
          <span>Export failed: {exportError.message}</span>
        </div>
      )}

      {/* ── 7-day analytics summary ── */}
      {!analyticsLoading && summary && total !== null && total > 0 && (
        <div className={analyticsBar}>
          <span className={analyticsTotal}>{total.toLocaleString()} decisions</span>
          <span style={{ color: "var(--text-muted)", fontSize: 12 }}>last 7 days</span>
          <span className={`${analyticsRate} ${rateAllow}`}>
            {autoAllowRate}% auto-allowed
          </span>
          <span className={`${analyticsRate} ${rateManual}`}>
            {manualRate}% manual review
          </span>
          {summary.topTools.length > 0 && (
            <span className={analyticsTopTool}>
              Top tool by decisions: {summary.topTools[0].toolName}
            </span>
          )}
        </div>
      )}

      {/* ── Suggested rule cards (Epic 3) ── */}
      {visibleSuggestions.length > 0 && (
        <div className={suggestionsContainer} data-testid="suggestions-container">
          {visibleSuggestions.map((suggestion, i) => (
            <SuggestedRuleCard
              key={i}
              suggestion={suggestion}
              onAccept={handleSuggestionAccept}
              onDiscard={() => handleSuggestionDiscard(suggestions.indexOf(suggestion))}
            />
          ))}
        </div>
      )}

      {/* ── Source filter tabs ── */}
      <div className={tabs}>
        {(["all", "user", "seed", "claude-settings"] as const).map((src) => {
          const count = src === "all" ? rules.length : rules.filter((r) => r.source === src).length;
          const fullLabel = src === "all" ? "All" : sourceLabel(src);
          const shortLabel = src === "claude-settings" ? "Settings" : fullLabel;
          return (
            <button
              key={src}
              className={`${tab} ${sourceFilter === src ? tabActive : ""}`}
              onClick={() => setSourceFilter(src)}
            >
              <span className={tabLabelFull}>{fullLabel}</span>
              <span className={tabLabelShort}>{shortLabel}</span>
              {" "}({count})
            </button>
          );
        })}
      </div>

      {/* ── Error ── */}
      {error && (
        <div className={errorClass}>
          Failed to load rules: {error.message}
          <button onClick={refresh} className={retryButton}>Retry</button>
        </div>
      )}

      {/* ── Rules table ── */}
      <div className={tableWrapper}>
        {loading && visibleRules.length === 0 ? (
          <div className={loadingClass}>Loading rules…</div>
        ) : visibleRules.length === 0 ? (
          <div className={empty} data-testid="empty-state">
            <p>
              Approval rules let you automatically allow or deny tool calls from Claude without manual review.
            </p>
            {(sourceFilter === "all" || sourceFilter === "user") && (
              <p>
                Use{" "}
                <button
                  style={{ background: "none", border: "none", cursor: "pointer", color: "inherit", textDecoration: "underline", padding: 0 }}
                  onClick={openForm}
                >
                  + Add Rule
                </button>
                {" "}to create a rule or{" "}
                <button
                  style={{ background: "none", border: "none", cursor: "pointer", color: "inherit", textDecoration: "underline", padding: 0 }}
                  onClick={() => setImportModalOpen(true)}
                >
                  Import YAML
                </button>
                {" "}to import from a file.
              </p>
            )}
            {sourceFilter === "seed" && (
              <p>No built-in rules are available in this workspace.</p>
            )}
            {sourceFilter === "claude-settings" && (
              <p>No rules from your ~/.claude/settings.json file were found.</p>
            )}
          </div>
        ) : (
          <table className={table}>
            <thead>
              <tr>
                <th className={th}>Name</th>
                <th className={th}>Match</th>
                <th className={th}>Decision</th>
                <th className={th}>Source</th>
                <th className={th} title="Lower numbers run first. Custom rules (default: 10) are evaluated before built-in rules (default: 1000).">Priority ⓘ</th>
                <th className={th}>Enabled</th>
                <th className={th}></th>
              </tr>
            </thead>
            <tbody>
              {visibleRules.map((rule) => (
                <tr key={rule.id} className={`${row} ${!rule.enabled ? rowDisabled : ""}`}>
                  <td className={td}>
                    <span className={ruleName}>{rule.name || rule.id}</span>
                    {rule.reason && (
                      <span className={ruleReason}>{rule.reason}</span>
                    )}
                    {rule.alternative && (
                      <span className={ruleAlt}>Alt: {rule.alternative}</span>
                    )}
                  </td>
                  <td className={td}>
                    <div className={matchInfo}>
                      {rule.toolName && <code className={matchChip} title={rule.toolName}>{rule.toolName}</code>}
                      {rule.criteriaPrograms && rule.criteriaPrograms.length > 0 && (
                        <code className={matchChip} title={`programs: ${rule.criteriaPrograms.join(", ")}`}>programs: {rule.criteriaPrograms.join(", ")}</code>
                      )}
                      {rule.criteriaSubcommands && rule.criteriaSubcommands.length > 0 && (
                        <code className={matchChip} title={`sub: ${rule.criteriaSubcommands.join(", ")}`}>sub: {rule.criteriaSubcommands.join(", ")}</code>
                      )}
                      {rule.commandPattern && <code className={matchChip} title={rule.commandPattern}>{rule.commandPattern}</code>}
                      {rule.toolPattern && <code className={matchChip} title={rule.toolPattern}>{rule.toolPattern}</code>}
                      {rule.filePattern && <code className={matchChip} title={rule.filePattern}>{rule.filePattern}</code>}
                    </div>
                  </td>
                  <td className={td}>
                    <span className={`${decisionBadge} ${decisionClass(rule.decision)}`}>
                      {decisionLabel(rule.decision)}
                    </span>
                  </td>
                  <td className={td}>
                    <span
                      className={sourceBadge}
                      title={
                        rule.source === "seed"
                          ? "These rules ship with stapler-squad and cannot be deleted"
                          : rule.source === "claude-settings"
                          ? "These rules come from your ~/.claude/settings.json file"
                          : undefined
                      }
                    >
                      {sourceLabel(rule.source)}
                    </span>
                  </td>
                  <td className={`${td} ${tdCenter}`}>{rule.priority}</td>
                  <td className={`${td} ${tdCenter}`}>
                    {rule.source === "user" ? (
                      <button
                        className={`${toggle} ${rule.enabled ? toggleOn : toggleOff}`}
                        onClick={() => handleToggle(rule)}
                        aria-label={rule.enabled ? "Disable rule" : "Enable rule"}
                      >
                        {rule.enabled ? "ON" : "OFF"}
                      </button>
                    ) : (
                      <span className={builtInBadge} title="Built-in rules cannot be disabled">
                        Always on
                      </span>
                    )}
                  </td>
                  <td className={`${td} ${tdCenter}`}>
                    {rule.source === "user" && (
                      <button
                        className={deleteButton}
                        onClick={() => deleteRule(rule.id)}
                        aria-label={`Delete rule ${rule.name}`}
                        title="Delete rule"
                      >
                        ✕
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* ── Row count indicator ── */}
      {visibleRules.length > 0 && (
        <div className={rowCount}>
          {visibleRules.length} rule{visibleRules.length !== 1 ? "s" : ""}
          {sourceFilter !== "all" && ` (filtered from ${rules.length} total)`}
        </div>
      )}

      {/* ── Mobile FAB — replaces the header "+ Add Rule" button on small screens ── */}
      <button
        className={mobileAddFab}
        onClick={openForm}
        aria-label="Add / Import rule"
        data-testid="add-rule-fab"
      >
        +
      </button>

      {/* ── Add Rule Modal — Modal handles portal, overlay, focus trap, Escape, body scroll ── */}
      <Modal open={showForm} onOpenChange={(open) => { if (!open) closeForm(); }}>
        <ModalContent
          showClose={false}
          className={ruleModalContent}
          onOpenAutoFocus={(e) => { e.preventDefault(); nameInputRef.current?.focus(); }}
        >
          <div className={modalHeader}>
            <div className={modalTitleRow}>
              <ModalTitle className={formTitle}>New Custom Rule</ModalTitle>
              {aiPrefilled && (
                <span className={aiGeneratedBadge} data-testid="ai-generated-badge">
                  AI-generated — review before saving
                </span>
              )}
            </div>
            <ModalClose className={modalCloseButton} aria-label="Close dialog">
              ×
            </ModalClose>
          </div>

          <div className={modalBody}>
              <div className={formClass}>
                {/* ── Epic 6: Generate from command (collapsible) ── */}
                <details className={commandSampleDetails} data-testid="generate-from-command-details">
                  <summary className={commandSampleSummary}>
                    Generate from command (optional)
                  </summary>
                  <div className={commandSampleBody}>
                    <textarea
                      className={commandSampleTextarea}
                      placeholder="Paste a raw command, e.g. git push origin main"
                      value={cmdSampleValue}
                      onChange={(e) => setCmdSampleValue(e.target.value)}
                      aria-label="Command sample"
                      data-testid="command-sample-textarea"
                      rows={2}
                    />
                    <div className={commandSampleActions}>
                      <button
                        className={generateButton}
                        type="button"
                        disabled={cmdGenLoading || !cmdSampleValue.trim()}
                        onClick={() => {
                          void cmdGenerate({
                            source: SuggestionSource.COMMAND_SAMPLE,
                            commandSample: cmdSampleValue.trim(),
                          });
                        }}
                        data-testid="command-sample-generate-button"
                      >
                        {cmdGenLoading ? "Generating…" : "Generate"}
                      </button>
                    </div>
                  </div>
                </details>

                {formError && <div className={formErrorClass}>{formError}</div>}

                {/* ── Section 1: Identity ── */}
                <div className={formSection}>
                  <div className={formSectionHeader}>Rule identity</div>
                  <div className={formGrid}>
                    <label className={label}>
                      Name *
                      <input
                        ref={nameInputRef}
                        className={input}
                        value={form.name}
                        onChange={(e) => setFormField("name", e.target.value)}
                        placeholder="e.g. Allow git log"
                        data-testid="form-name-input"
                      />
                    </label>

                    <label className={label}>
                      Decision *
                      <select
                        className={select}
                        value={form.decision}
                        onChange={(e) => setFormField("decision", Number(e.target.value) as AutoDecision)}
                      >
                        <option value={AutoDecision.ALLOW}>Auto-Allow</option>
                        <option value={AutoDecision.DENY}>Auto-Deny</option>
                        <option value={AutoDecision.ESCALATE}>Escalate (manual)</option>
                      </select>
                    </label>

                    <label className={label}>
                      Priority
                      <input
                        className={input}
                        type="number"
                        min={1}
                        max={999}
                        value={form.priority}
                        onChange={(e) => setFormField("priority", Number(e.target.value))}
                      />
                      <span className={priorityHint}>Lower numbers run first. Custom rules (default: 10) run before built-in rules (default: 1000).</span>
                    </label>
                  </div>
                </div>

                {/* ── Section 2: Match conditions — structured fields first ── */}
                <div className={formSection}>
                  <div className={formSectionHeader}>Match conditions</div>
                  <div className={formGrid}>
                    <label className={label}>
                      Tool Name
                      <input
                        className={input}
                        value={form.toolName}
                        onChange={(e) => setFormField("toolName", e.target.value)}
                        placeholder="e.g. Bash"
                        data-testid="form-tool-name-input"
                      />
                    </label>

                    <label className={label}>
                      Programs
                      <input
                        className={input}
                        value={form.criteriaPrograms.join(", ")}
                        onChange={(e) => setFormField("criteriaPrograms", e.target.value.split(",").map((s) => s.trim()).filter(Boolean))}
                        placeholder="e.g. git, gh, npm"
                        data-testid="form-criteria-programs-input"
                      />
                      <span className={priorityHint}>Preferred over Command Pattern for program/subcommand rules.</span>
                    </label>

                    <label className={label}>
                      Subcommands
                      <input
                        className={input}
                        value={form.criteriaSubcommands.join(", ")}
                        onChange={(e) => setFormField("criteriaSubcommands", e.target.value.split(",").map((s) => s.trim()).filter(Boolean))}
                        placeholder="e.g. push, publish, deploy"
                        data-testid="form-criteria-subcommands-input"
                      />
                    </label>

                    {/* Separator before advanced regex patterns */}
                    <div style={{ gridColumn: "1 / -1" }}>
                      <hr style={{ border: "none", borderTop: `1px solid var(--border-color)`, margin: "4px 0 8px" }} />
                      <span className={formSectionHeader} data-testid="advanced-regex-separator">
                        Advanced: regex patterns
                      </span>
                      <p className={priorityHint} style={{ marginTop: 4 }}>
                        Regex patterns are powerful but hard to maintain. Use the fields above when possible.
                      </p>
                    </div>

                    <label className={label}>
                      Command Pattern (regex)
                      <input
                        className={input}
                        value={form.commandPattern}
                        onChange={(e) => setFormField("commandPattern", e.target.value)}
                        placeholder="e.g. ^git log"
                        data-testid="form-command-pattern-input"
                      />
                    </label>

                    <label className={label}>
                      Tool Pattern (regex)
                      <input
                        className={input}
                        value={form.toolPattern}
                        onChange={(e) => setFormField("toolPattern", e.target.value)}
                        placeholder="e.g. Read|Glob"
                      />
                    </label>

                    <label className={label}>
                      File Pattern (regex)
                      <input
                        className={input}
                        value={form.filePattern}
                        onChange={(e) => setFormField("filePattern", e.target.value)}
                        placeholder="e.g. \.md$"
                      />
                    </label>
                  </div>
                </div>

                {/* ── Section 3: Guidance (for Claude when rule triggers) ── */}
                <div className={formSection}>
                  <div className={formSectionHeader}>Guidance for Claude</div>
                  <div className={formGrid}>
                    <label className={label}>
                      Reason
                      <input
                        className={input}
                        value={form.reason}
                        onChange={(e) => setFormField("reason", e.target.value)}
                        placeholder="Shown to Claude when denied"
                      />
                    </label>

                    <label className={label}>
                      Alternative
                      <input
                        className={input}
                        value={form.alternative}
                        onChange={(e) => setFormField("alternative", e.target.value)}
                        placeholder="Safer command suggestion"
                      />
                    </label>
                  </div>
                </div>

                <div className={formActions}>
                  <button
                    className={saveButton}
                    onClick={handleSave}
                    disabled={saving}
                  >
                    {saving ? "Saving…" : "Save Rule"}
                  </button>
                  <button
                    className={cancelButton}
                    onClick={closeForm}
                  >
                    Cancel
                  </button>
                </div>
              </div>
            </div>
        </ModalContent>
      </Modal>

      {/* ── Import Rules Modal ── */}
      <ImportRulesModal
        open={importModalOpen}
        onClose={() => setImportModalOpen(false)}
        onApplied={() => { void refresh(); }}
        existingRules={rules}
      />
    </div>
  );
}
