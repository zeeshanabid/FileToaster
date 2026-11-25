import { describe, it, expect, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import {
  searchSessions,
  beginSearch,
  appendSearchResult,
  completeSearch,
  removeSearch,
  getSearchSession,
  defaultAdvancedFilters,
  hasAdvancedFilters,
  buildSearchRequest,
  type AdvancedFilters
} from '$lib/store/search';
import type { SearchResultItem } from '$lib/fs/api';

function item(name: string, overrides: Partial<SearchResultItem> = {}): SearchResultItem {
  return {
    path: `/root/${name}`,
    name,
    type: 'file',
    size: 10,
    created: '2024-01-01T00:00:00Z',
    modified: '2024-01-01T00:00:00Z',
    ...overrides
  };
}

beforeEach(() => {
  searchSessions.set({});
});

describe('search session store', () => {
  it('begins a running session with empty results', () => {
    beginSearch('t1', { rootPath: '/root', query: 'foo' });
    const session = getSearchSession('t1');
    expect(session).toBeDefined();
    expect(session!.status).toBe('running');
    expect(session!.results).toEqual([]);
    expect(session!.query).toBe('foo');
    expect(session!.rootPath).toBe('/root');
  });

  it('ignores a blank task id', () => {
    beginSearch('', { rootPath: '/root', query: 'x' });
    expect(get(searchSessions)).toEqual({});
  });

  it('appends results and tracks the match count', () => {
    beginSearch('t1', { rootPath: '/root', query: 'a' });
    appendSearchResult('t1', item('a.txt'));
    appendSearchResult('t1', item('b.txt'));
    const session = getSearchSession('t1')!;
    expect(session.results.map((r) => r.name)).toEqual(['a.txt', 'b.txt']);
    expect(session.matches).toBe(2);
  });

  it('appends a late result even after the session completed', () => {
    beginSearch('t1', { rootPath: '/root', query: 'a' });
    completeSearch('t1', 'finished', undefined, { scanned: 5, matches: 1 });
    appendSearchResult('t1', item('late.txt'));
    const session = getSearchSession('t1')!;
    expect(session.status).toBe('finished');
    expect(session.results.map((r) => r.name)).toEqual(['late.txt']);
    expect(session.scanned).toBe(5);
  });

  it('drops results for an unknown session', () => {
    appendSearchResult('nope', item('a.txt'));
    expect(get(searchSessions)).toEqual({});
  });

  it('completes with totals and an error', () => {
    beginSearch('t1', { rootPath: '/root', query: 'a' });
    completeSearch('t1', 'failed', 'boom', { scanned: 3, matches: 0 });
    const session = getSearchSession('t1')!;
    expect(session.status).toBe('failed');
    expect(session.error).toBe('boom');
    expect(session.scanned).toBe(3);
  });

  it('removes a session', () => {
    beginSearch('t1', { rootPath: '/root', query: 'a' });
    removeSearch('t1');
    expect(getSearchSession('t1')).toBeUndefined();
  });
});

describe('advanced filters', () => {
  it('defaults to no filters', () => {
    const filters = defaultAdvancedFilters();
    expect(hasAdvancedFilters(filters)).toBe(false);
  });

  it('detects each kind of active filter', () => {
    const base = defaultAdvancedFilters();
    expect(hasAdvancedFilters({ ...base, mimeTypes: ['image/*'] })).toBe(true);
    expect(hasAdvancedFilters({ ...base, extensions: ['txt'] })).toBe(true);
    expect(hasAdvancedFilters({ ...base, createdPreset: '7d' })).toBe(true);
    expect(hasAdvancedFilters({ ...base, modifiedPreset: '1h' })).toBe(true);
    expect(hasAdvancedFilters({ ...base, minSize: 1 })).toBe(true);
    expect(hasAdvancedFilters({ ...base, maxSize: 1 })).toBe(true);
  });

  it('builds a request with normalized extensions and converted sizes', () => {
    const filters: AdvancedFilters = {
      ...defaultAdvancedFilters(),
      mimeTypes: ['image/*'],
      extensions: ['.TXT', ' Log '],
      minSize: 2,
      maxSize: 4,
      sizeUnit: 'MB'
    };
    const req = buildSearchRequest(filters, '  hello  ', '/root');
    expect(req.rootPath).toBe('/root');
    expect(req.query).toBe('hello');
    expect(req.mimeTypes).toEqual(['image/*']);
    expect(req.extensions).toEqual(['txt', 'log']);
    expect(req.minSize).toBe(2 * 1024 * 1024);
    expect(req.maxSize).toBe(4 * 1024 * 1024);
    expect(req.createdAfter).toBe('');
    expect(req.createdBefore).toBe('');
  });

  it('expands date presets into an ISO range anchored at now', () => {
    const now = new Date('2024-06-15T12:00:00Z');
    const filters: AdvancedFilters = { ...defaultAdvancedFilters(), createdPreset: '1d' };
    const req = buildSearchRequest(filters, 'q', '/root', now);
    expect(req.createdBefore).toBe(now.toISOString());
    expect(req.createdAfter).toBe(new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString());
  });

  it('passes a custom date range through verbatim', () => {
    const filters: AdvancedFilters = {
      ...defaultAdvancedFilters(),
      createdPreset: 'custom',
      createdAfter: '2024-01-01T10:00',
      createdBefore: '2024-02-01T10:00'
    };
    const req = buildSearchRequest(filters, 'q', '/root');
    expect(req.createdAfter).toBe('2024-01-01T10:00');
    expect(req.createdBefore).toBe('2024-02-01T10:00');
  });
});
