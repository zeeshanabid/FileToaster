import { writable, get } from 'svelte/store';
import type { SearchRequest, SearchResultItem } from '$lib/fs/api';
import {
  presetToDateRange,
  sizeToBytes,
  normalizeExtension,
  type DatePreset,
  type SizeUnit
} from '$lib/fs/format';

// AdvancedFilters is the advanced-search dialog's form state. Date filters use a
// relative preset (converted to an ISO range at search time) or an explicit
// custom range; the size range shares one unit selector.
export interface AdvancedFilters {
  mimeTypes: string[];
  extensions: string[];
  createdPreset: DatePreset;
  createdAfter: string;
  createdBefore: string;
  modifiedPreset: DatePreset;
  modifiedAfter: string;
  modifiedBefore: string;
  minSize: number;
  maxSize: number;
  sizeUnit: SizeUnit;
}

// defaultAdvancedFilters returns the empty filter set (nothing restricted).
export function defaultAdvancedFilters(): AdvancedFilters {
  return {
    mimeTypes: [],
    extensions: [],
    createdPreset: 'all',
    createdAfter: '',
    createdBefore: '',
    modifiedPreset: 'all',
    modifiedAfter: '',
    modifiedBefore: '',
    minSize: 0,
    maxSize: 0,
    sizeUnit: 'MB'
  };
}

// hasAdvancedFilters reports whether any filter is active, i.e. whether applying
// the dialog would actually narrow the search.
export function hasAdvancedFilters(f: AdvancedFilters): boolean {
  return (
    f.mimeTypes.length > 0 ||
    f.extensions.length > 0 ||
    f.createdPreset !== 'all' ||
    f.modifiedPreset !== 'all' ||
    f.minSize > 0 ||
    f.maxSize > 0
  );
}

// customOrPreset keeps an explicit custom range when the preset is 'custom',
// otherwise uses the preset's computed [after, before] range.
function customOrPreset(
  preset: DatePreset,
  customAfter: string,
  customBefore: string,
  now: Date
): { after: string; before: string } {
  if (preset === 'custom') return { after: customAfter, before: customBefore };
  return presetToDateRange(preset, now);
}

// buildSearchRequest converts the dialog's filter state plus the name query and
// search root into the server request. Extensions are normalized, sizes are
// converted from the selected unit into bytes, and date presets are expanded to
// an ISO range (custom ranges are sent verbatim).
export function buildSearchRequest(
  filters: AdvancedFilters,
  query: string,
  rootPath: string,
  now: Date = new Date()
): SearchRequest {
  const created = customOrPreset(
    filters.createdPreset,
    filters.createdAfter,
    filters.createdBefore,
    now
  );
  const modified = customOrPreset(
    filters.modifiedPreset,
    filters.modifiedAfter,
    filters.modifiedBefore,
    now
  );
  return {
    rootPath,
    query: query.trim(),
    mimeTypes: [...filters.mimeTypes],
    extensions: filters.extensions.map(normalizeExtension).filter(Boolean),
    minSize: sizeToBytes(filters.minSize, filters.sizeUnit),
    maxSize: sizeToBytes(filters.maxSize, filters.sizeUnit),
    createdAfter: created.after,
    createdBefore: created.before,
    modifiedAfter: modified.after,
    modifiedBefore: modified.before
  };
}

// Per-search session state. A search is a tracked task: the window submits one
// (POST /api/search) and then reads matches as they stream in over SSE. The
// session registry is keyed by task id so a single global SSE connection can
// route each "search-result" event to the window that owns it. The window keeps
// its own task id and derives its view from this store.
export interface SearchSession {
  taskID: string;
  rootPath: string;
  query: string;
  results: SearchResultItem[];
  scanned: number;
  matches: number;
  status: 'running' | 'finished' | 'failed' | 'cancelled';
  error?: string;
}

// searchSessions maps a search task id to its live session. Components read the
// session for their own task id; appendSearchResult/completeSearch update it,
// which re-renders them reactively.
export const searchSessions = writable<Record<string, SearchSession>>({});

// beginSearch registers a freshly submitted search. It must be called with the
// returned task id before SSE results can be routed.
export function beginSearch(taskID: string, opts: { rootPath: string; query: string }): void {
  if (!taskID) return;
  searchSessions.update((map) => ({
    ...map,
    [taskID]: {
      taskID,
      rootPath: opts.rootPath,
      query: opts.query,
      results: [],
      scanned: 0,
      matches: 0,
      status: 'running'
    }
  }));
}

// appendSearchResult adds one streamed match to the session. Late results are
// appended even after the session reached a terminal status: the task-completion
// delta and the result events travel on separate SSE channels, so a terminal
// event can (rarely) be processed before the last buffered matches arrive.
export function appendSearchResult(taskID: string, item: SearchResultItem): void {
  if (!taskID || !item) return;
  searchSessions.update((map) => {
    const session = map[taskID];
    if (!session) return map;
    const results = [...session.results, item];
    return { ...map, [taskID]: { ...session, results, matches: results.length } };
  });
}

// completeSearch marks a search terminal. `totals` (when supplied by the
// search-complete event) refreshes the scanned/matches counters.
export function completeSearch(
  taskID: string,
  status: SearchSession['status'],
  error?: string,
  totals?: { scanned?: number; matches?: number }
): void {
  if (!taskID) return;
  searchSessions.update((map) => {
    const session = map[taskID];
    if (!session) return map;
    return {
      ...map,
      [taskID]: {
        ...session,
        status,
        error: error ?? session.error,
        scanned: totals?.scanned ?? session.scanned,
        matches: totals?.matches ?? session.matches
      }
    };
  });
}

// removeSearch drops a session (window closed or search cancelled).
export function removeSearch(taskID: string): void {
  if (!taskID) return;
  searchSessions.update((map) => {
    if (!(taskID in map)) return map;
    const next = { ...map };
    delete next[taskID];
    return next;
  });
}

// getSearchSession returns the current session for a task id (non-reactive).
export function getSearchSession(taskID: string): SearchSession | undefined {
  return get(searchSessions)[taskID];
}
