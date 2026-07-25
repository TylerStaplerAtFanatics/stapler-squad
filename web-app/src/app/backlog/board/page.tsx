// analytics-exempt
"use client";
// +feature: backlog:board-page

import { useState, useEffect, useCallback } from "react";
import { useRouter } from "next/navigation";
import { BacklogBoard } from "@/components/backlog/BacklogBoard";
import { useBacklogService } from "@/lib/hooks/useBacklogService";
import type { BacklogItem } from "@/lib/hooks/useBacklogService";

export default function BacklogBoardPage() {
  const { listBacklogItems, transitionStatus, triggerTriage, spawnSessionFromItem } = useBacklogService();
  const router = useRouter();
  const [items, setItems] = useState<BacklogItem[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await listBacklogItems();
      setItems(result);
    } finally {
      setLoading(false);
    }
  }, [listBacklogItems]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleAction = useCallback(
    async (action: string, itemId: string) => {
      switch (action) {
        case "mark_ready":
          await transitionStatus(itemId, "ready");
          break;
        case "trigger_triage":
          await triggerTriage(itemId);
          break;
        case "spawn_session":
          await spawnSessionFromItem(itemId);
          break;
        case "view_session":
        case "view_review":
          router.push(`/backlog?item=${itemId}`);
          break;
        default:
          break;
      }
      await load();
    },
    [transitionStatus, triggerTriage, spawnSessionFromItem, load, router]
  );

  const handleItemClick = useCallback(
    (itemId: string) => {
      router.push(`/backlog?item=${itemId}`);
    },
    [router]
  );

  return (
    <BacklogBoard
      items={items}
      onAction={handleAction}
      onItemClick={handleItemClick}
      isLoading={loading}
    />
  );
}
