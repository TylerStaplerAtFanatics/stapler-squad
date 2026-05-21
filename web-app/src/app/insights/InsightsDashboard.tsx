// +feature: insights-dashboard
"use client";

import dynamic from "next/dynamic";
import { useInsightsSummary } from "@/lib/hooks/useInsightsService";
import { SummaryCards } from "./SummaryCards";
import { TopNTable } from "./TopNTables";
import { SessionsTable } from "./SessionsTable";

// Lazy-load recharts and its D3 dependencies (~1.2MB) only when the insights page is visited.
const DailySpendChart = dynamic(() => import("./DailySpendChart").then((m) => m.DailySpendChart), { ssr: false });
const ModelBreakdownChart = dynamic(() => import("./ModelBreakdownChart").then((m) => m.ModelBreakdownChart), { ssr: false });
const ModelOverTimeChart = dynamic(() => import("./ModelOverTimeChart").then((m) => m.ModelOverTimeChart), { ssr: false });
import {
  page,
  pageHeader,
  title,
  subtitle,
  liveIndicator,
  liveDot,
  loadingBanner,
  spinner,
  errorBox,
  emptyState,
  grid2,
  section,
  sectionTitle,
} from "./InsightsDashboard.css";

export function InsightsDashboard() {
  const { summary, loading, isLiveUpdating, error } = useInsightsSummary({
    includeOrphans: true,
  });

  return (
    <div className={page}>
      <div className={pageHeader}>
        <div>
          <h1 className={title}>Insights</h1>
          <p className={subtitle}>Token usage analytics and cost breakdown</p>
        </div>
        {isLiveUpdating && (
          <div className={liveIndicator}>
            <div className={liveDot} />
            Live
          </div>
        )}
      </div>

      {error && <div className={errorBox}>{error}</div>}

      {loading && !summary && (
        <div className={loadingBanner}>
          <div className={spinner} />
          Loading token data…
        </div>
      )}

      {summary?.isLoading && (
        <div className={loadingBanner}>
          <div className={spinner} />
          Parsing conversation history in the background…
        </div>
      )}

      {!loading && !error && summary && summary.sessions.length === 0 && (
        <div className={emptyState}>
          No token usage data found. Run some Claude Code sessions to see analytics here.
        </div>
      )}

      {summary && summary.sessions.length > 0 && (
        <>
          <section className={section}>
            <SummaryCards summary={summary} />
          </section>

          <section className={section}>
            <div className={grid2}>
              <DailySpendChart daily={summary.daily} />
              <ModelBreakdownChart models={summary.models} />
            </div>
          </section>

          <section className={section}>
            <ModelOverTimeChart daily={summary.daily} mode="cost" />
          </section>

          {(summary.topSkills.length > 0 || summary.topTools.length > 0) && (
            <section className={section}>
              <h2 className={sectionTitle}>Top Usage</h2>
              <div className={grid2}>
                {summary.topSkills.length > 0 && (
                  <TopNTable
                    title="Top Skills"
                    entries={summary.topSkills}
                    valueLabel="Tokens"
                  />
                )}
                {summary.topTools.length > 0 && (
                  <TopNTable
                    title="Top Tools"
                    entries={summary.topTools}
                    valueLabel="Tokens"
                  />
                )}
              </div>
            </section>
          )}

          <section className={section}>
            <h2 className={sectionTitle}>Sessions</h2>
            <SessionsTable sessions={summary.sessions} />
          </section>
        </>
      )}
    </div>
  );
}
