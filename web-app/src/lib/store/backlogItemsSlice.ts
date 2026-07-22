import { createSlice, createSelector, PayloadAction } from "@reduxjs/toolkit";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { BacklogItem } from "@/gen/session/v1/backlog_pb";
import type { RootState } from "./store";

interface BacklogItemsState {
  /** Normalized map of backlog items, keyed by item id. */
  items: Record<string, BacklogItem>;
}

const initialState: BacklogItemsState = {
  items: {},
};

/** Returns the epoch-ms value of a proto Timestamp, or 0 if unset (oldest possible). */
function timestampMs(ts: BacklogItem["updatedAt"]): number {
  return ts ? timestampDate(ts).getTime() : 0;
}

const backlogItemsSlice = createSlice({
  name: "backlogItems",
  initialState,
  reducers: {
    /**
     * Upserts a backlog item, guarded by a real `updatedAt`-based staleness
     * check (not sessionsSlice.upsertSession's equal-only check): an incoming
     * item strictly older than the currently-stored item for the same id is
     * dropped, so out-of-order event delivery (e.g. concurrent publishers,
     * stream replay) can never regress the store to older data.
     */
    upsertItem(state, action: PayloadAction<BacklogItem>) {
      const incoming = action.payload;
      const existing = state.items[incoming.id];
      if (existing && timestampMs(incoming.updatedAt) < timestampMs(existing.updatedAt)) {
        return;
      }
      state.items[incoming.id] = incoming;
    },
    /** Deletes an item from the map entirely (BacklogItemRemovedEvent — permanent delete, not upsert). */
    removeItem(state, action: PayloadAction<string>) {
      delete state.items[action.payload];
    },
  },
});

export const { upsertItem, removeItem } = backlogItemsSlice.actions;

// Base selectors
export const selectBacklogItemsMap = (state: RootState) => state.backlogItems.items;
export const selectBacklogItemById = (state: RootState, id: string): BacklogItem | undefined =>
  state.backlogItems.items[id];

// Memoized selectors: list-shaped reads off backlogItemsSlice must not allocate
// a new array/object on every call, or every consumer (e.g. BacklogItemCard)
// re-renders whenever ANY item in the store changes, not just its own.
export const selectAllBacklogItems = createSelector(
  selectBacklogItemsMap,
  (items) => Object.values(items)
);

export const selectBacklogItemsTotal = createSelector(
  selectBacklogItemsMap,
  (items) => Object.keys(items).length
);

/** Shallow array equality: same length and same element references, in order. */
function shallowArrayEqual<T>(a: T[], b: T[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

/**
 * Selector factory for a status-filtered view. Use one instance per
 * component (e.g. via `useMemo(makeSelectBacklogItemsByStatus, [])`) rather
 * than a single shared selector — a shared selector's single-entry cache
 * would thrash (and stop memoizing) if multiple components filter by
 * different statuses simultaneously.
 *
 * `resultEqualityCheck: shallowArrayEqual` is what actually delivers on the
 * "unrelated item update shouldn't re-render this list" guarantee: without
 * it, the filtered array is a brand-new reference every time ANY item in the
 * store changes (because `selectAllBacklogItems` itself must return a new
 * array whenever the underlying map changes), even if none of the matching
 * items actually differ. With it, the selector still recomputes, but returns
 * the previous array reference when the filtered contents are unchanged.
 */
export const makeSelectBacklogItemsByStatus = () =>
  createSelector(
    [selectAllBacklogItems, (_state: RootState, status: string) => status],
    (items, status) => items.filter((item) => item.status === status),
    { memoizeOptions: { resultEqualityCheck: shallowArrayEqual } }
  );

export default backlogItemsSlice.reducer;
