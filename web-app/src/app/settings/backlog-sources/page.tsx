// analytics-exempt
// +feature: settings-backlog-sources
"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useFeatureFlags } from "@/lib/contexts/FeatureFlagsContext";
import { BacklogSourcesSettings } from "@/components/settings/BacklogSourcesSettings";
import { PageViewTracker } from "@/components/analytics/PageViewTracker";

export default function BacklogSourcesSettingsPage() {
  const { flags, isLoading } = useFeatureFlags();
  const router = useRouter();

  useEffect(() => {
    if (!isLoading && !flags["backlog"]) {
      router.replace("/");
    }
  }, [isLoading, flags, router]);

  if (isLoading) return null;
  if (!flags["backlog"]) return null;

  return (
    <>
      <PageViewTracker />
      <BacklogSourcesSettings />
    </>
  );
}
