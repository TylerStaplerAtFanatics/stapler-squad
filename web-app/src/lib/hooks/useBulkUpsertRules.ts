"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import {
  SessionService,
  BulkUpsertRulesRequestSchema,
  type BulkUpsertRulesResponse,
} from "@/gen/session/v1/session_pb";
import { type ApprovalRuleProto } from "@/gen/session/v1/types_pb";
import { create } from "@bufbuild/protobuf";
import { getConnectTransport } from "@/lib/api/transport";

export interface UseBulkUpsertRulesReturn {
  applyRules: (rules: ApprovalRuleProto[], overwriteDuplicates: boolean) => Promise<void>;
  loading: boolean;
  result: BulkUpsertRulesResponse | null;
  error: Error | null;
}

/**
 * Hook that calls BulkUpsertRules and returns apply results.
 * - Returns created/updated/skipped counts and per-rule errors.
 * - Loading state is managed automatically.
 */
export function useBulkUpsertRules(): UseBulkUpsertRulesReturn {
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<BulkUpsertRulesResponse | null>(null);
  const [error, setError] = useState<Error | null>(null);

  // Initialize client inside useEffect to avoid SSR issues.
  const clientRef = useRef<ReturnType<typeof createClient<typeof SessionService>> | null>(null);

  useEffect(() => {
    clientRef.current = createClient(SessionService, getConnectTransport());
  }, []);

  const applyRules = useCallback(async (rules: ApprovalRuleProto[], overwriteDuplicates: boolean) => {
    if (!clientRef.current) return;
    setLoading(true);
    setError(null);
    setResult(null);
    try {
      const req = create(BulkUpsertRulesRequestSchema, {
        rules,
        overwriteDuplicates,
      });
      const resp = await clientRef.current.bulkUpsertRules(req);
      setResult(resp);
    } catch (err) {
      setError(err instanceof Error ? err : new Error("Apply rules failed"));
    } finally {
      setLoading(false);
    }
  }, []);

  return { applyRules, loading, result, error };
}
