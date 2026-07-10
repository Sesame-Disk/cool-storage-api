import React, { useState, useEffect, useRef, useCallback } from 'react';
import { Search, X, Clock, SlidersHorizontal } from 'lucide-react';
import { searchFiles, listRepos } from '../../lib/api';
import type { SearchOptions } from '../../lib/api';
import type { SearchResult, Repo } from '../../lib/models';
import FileIcon from '../ui/FileIcon';

/** Advanced-search filters. `objType` maps to the backend `type` param and
 *  `repoId` to `repo_id`. Empty strings mean "no filter" (all types / all libs). */
type ObjTypeFilter = '' | 'file' | 'dir';
interface Filters {
  objType: ObjTypeFilter;
  repoId: string;
}
const DEFAULT_FILTERS: Filters = { objType: '', repoId: '' };

function filtersToOptions(f: Filters): SearchOptions {
  const opts: SearchOptions = {};
  if (f.objType) opts.objType = f.objType;
  if (f.repoId) opts.repoId = f.repoId;
  return opts;
}

function filtersActive(f: Filters): boolean {
  return !!f.objType || !!f.repoId;
}

const RECENT_SEARCHES_KEY = 'recent_searches';
const MAX_RECENT = 10;
const DEBOUNCE_MS = 300;
const MIN_QUERY_LENGTH = 2;
const PER_PAGE = 25;

function getFileType(name: string): 'folder' | 'image' | 'video' | 'pdf' | 'doc' | 'code' | 'audio' | 'archive' | 'generic' {
  const ext = name.split('.').pop()?.toLowerCase() || '';
  if (['jpg', 'jpeg', 'png', 'gif', 'bmp', 'svg', 'webp'].includes(ext)) return 'image';
  if (['mp4', 'avi', 'mov', 'mkv', 'webm'].includes(ext)) return 'video';
  if (ext === 'pdf') return 'pdf';
  if (['doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'txt', 'md'].includes(ext)) return 'doc';
  if (['js', 'ts', 'py', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'css', 'html', 'json', 'yaml', 'yml', 'xml'].includes(ext)) return 'code';
  if (['mp3', 'wav', 'flac', 'aac', 'ogg'].includes(ext)) return 'audio';
  if (['zip', 'tar', 'gz', 'rar', '7z'].includes(ext)) return 'archive';
  return 'generic';
}

function HighlightedText({ text, query }: { text: string; query: string }) {
  if (!query) return <>{text}</>;
  const regex = new RegExp(`(${query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
  const parts = text.split(regex);
  return (
    <>
      {parts.map((part, i) =>
        regex.test(part) ? (
          <mark key={i} className="bg-yellow-200 text-inherit rounded-sm px-0.5">{part}</mark>
        ) : (
          <span key={i}>{part}</span>
        )
      )}
    </>
  );
}

function loadRecentSearches(): string[] {
  try {
    const stored = localStorage.getItem(RECENT_SEARCHES_KEY);
    return stored ? JSON.parse(stored) : [];
  } catch {
    return [];
  }
}

function saveRecentSearches(searches: string[]): void {
  localStorage.setItem(RECENT_SEARCHES_KEY, JSON.stringify(searches.slice(0, MAX_RECENT)));
}

export default function SearchPage() {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<SearchResult[]>([]);
  const [recentSearches, setRecentSearches] = useState<string[]>(loadRecentSearches);
  const [loading, setLoading] = useState(false);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [showFilters, setShowFilters] = useState(false);
  const [filters, setFilters] = useState<Filters>(DEFAULT_FILTERS);
  const [repos, setRepos] = useState<Repo[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const sentinelRef = useRef<HTMLDivElement>(null);
  const filtersRef = useRef<Filters>(filters);
  filtersRef.current = filters;

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // Load libraries lazily the first time the filter panel is opened (for the
  // search-scope selector). Failure is non-fatal — scope just stays "all".
  useEffect(() => {
    if (!showFilters || repos.length > 0) return;
    let cancelled = false;
    listRepos()
      .then((r) => { if (!cancelled) setRepos(r); })
      .catch(() => { /* scope selector degrades to All libraries */ });
    return () => { cancelled = true; };
  }, [showFilters, repos.length]);

  const performSearch = useCallback(async (q: string, pageNum: number = 1) => {
    if (q.length < MIN_QUERY_LENGTH) {
      setResults([]);
      setTotal(0);
      setHasMore(false);
      return;
    }

    if (pageNum === 1) {
      setLoading(true);
    } else {
      setLoadingMore(true);
    }

    try {
      const data = await searchFiles(q, pageNum, PER_PAGE, filtersToOptions(filtersRef.current));
      if (pageNum === 1) {
        setResults(data.results);
      } else {
        setResults(prev => [...prev, ...data.results]);
      }
      setTotal(data.total);
      setPage(pageNum);
      setHasMore(pageNum * PER_PAGE < data.total);
    } catch {
      // silently fail for search
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }, []);

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);

    if (query.length < MIN_QUERY_LENGTH) {
      setResults([]);
      setTotal(0);
      setHasMore(false);
      return;
    }

    debounceRef.current = setTimeout(() => {
      performSearch(query, 1);
    }, DEBOUNCE_MS);

    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
    // Re-search from page 1 whenever the query OR the active filters change.
  }, [query, filters, performSearch]);

  // Infinite scroll via IntersectionObserver
  useEffect(() => {
    if (!hasMore || loadingMore) return;
    const sentinel = sentinelRef.current;
    if (!sentinel) return;

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting) {
          performSearch(query, page + 1);
        }
      },
      { threshold: 0.1 }
    );
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [hasMore, loadingMore, query, page, performSearch]);

  const addToRecent = (q: string) => {
    const updated = [q, ...recentSearches.filter(s => s !== q)].slice(0, MAX_RECENT);
    setRecentSearches(updated);
    saveRecentSearches(updated);
  };

  const removeRecentSearch = (q: string) => {
    const updated = recentSearches.filter(s => s !== q);
    setRecentSearches(updated);
    saveRecentSearches(updated);
  };

  const clearAllRecent = () => {
    setRecentSearches([]);
    localStorage.removeItem(RECENT_SEARCHES_KEY);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (query.length >= MIN_QUERY_LENGTH) {
      addToRecent(query);
      performSearch(query, 1);
    }
  };

  const handleRecentClick = (q: string) => {
    setQuery(q);
    addToRecent(q);
  };

  const handleCancel = () => {
    setQuery('');
    setResults([]);
    inputRef.current?.focus();
  };

  const handleResultClick = (result: SearchResult) => {
    addToRecent(query);
    if (result.is_dir) {
      window.location.href = `/library/${result.repo_id}${result.path}`;
    } else {
      window.location.href = `/library/${result.repo_id}/file${result.path}`;
    }
  };

  // Group results by library
  const groupedResults = results.reduce<Record<string, SearchResult[]>>((groups, result) => {
    const key = result.repo_name;
    if (!groups[key]) groups[key] = [];
    groups[key].push(result);
    return groups;
  }, {});

  const showRecent = query.length < MIN_QUERY_LENGTH && recentSearches.length > 0;

  return (
    <div className="flex flex-col h-full min-h-screen bg-white" data-testid="search-page">
      {/* Search header */}
      <form onSubmit={handleSubmit} className="flex items-center gap-2 p-3 border-b border-gray-200 bg-white sticky top-0 z-10">
        <div className="flex-1 flex items-center gap-2 bg-gray-100 rounded-lg px-3 py-2">
          <Search className="w-5 h-5 text-gray-400 shrink-0" />
          <input
            ref={inputRef}
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search files..."
            className="flex-1 bg-transparent outline-none text-base text-text placeholder-gray-400"
            data-testid="search-input"
          />
          {query && (
            <button
              type="button"
              onClick={() => setQuery('')}
              className="p-0.5"
              data-testid="clear-query"
            >
              <X className="w-4 h-4 text-gray-400" />
            </button>
          )}
        </div>
        <button
          type="button"
          onClick={() => setShowFilters((v) => !v)}
          aria-label="Toggle search filters"
          aria-expanded={showFilters}
          className={`relative shrink-0 p-2 rounded-lg ${showFilters || filtersActive(filters) ? 'text-blue-500 bg-blue-50' : 'text-gray-500'}`}
          data-testid="search-filter-toggle"
        >
          <SlidersHorizontal className="w-5 h-5" />
          {filtersActive(filters) && (
            <span className="absolute top-1 right-1 w-2 h-2 bg-blue-500 rounded-full" data-testid="filter-active-dot" />
          )}
        </button>
        {/* Explicit submit control (Enter also submits the form). */}
        <button type="submit" className="sr-only" data-testid="search-submit">Search</button>
        <button
          type="button"
          onClick={handleCancel}
          className="text-blue-500 text-sm font-medium shrink-0"
          data-testid="cancel-button"
        >
          Cancel
        </button>
      </form>

      {/* Advanced filter panel (inline — no overlay, avoids z-index conflicts) */}
      {showFilters && (
        <div className="p-3 border-b border-gray-200 bg-gray-50 space-y-3" data-testid="search-filters">
          <div>
            <p className="text-xs font-semibold text-gray-500 uppercase mb-1.5">Show</p>
            <div className="flex gap-2" role="radiogroup" aria-label="File type filter">
              {([
                { value: '', label: 'All' },
                { value: 'file', label: 'Files' },
                { value: 'dir', label: 'Folders' },
              ] as const).map((opt) => (
                <button
                  key={opt.value || 'all'}
                  type="button"
                  role="radio"
                  aria-checked={filters.objType === opt.value}
                  onClick={() => setFilters((f) => ({ ...f, objType: opt.value }))}
                  className={`px-3 py-1.5 rounded-full text-sm border ${
                    filters.objType === opt.value
                      ? 'bg-blue-500 text-white border-blue-500'
                      : 'bg-white text-text border-gray-300'
                  }`}
                  data-testid={`filter-type-${opt.value || 'all'}`}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>

          <div>
            <p className="text-xs font-semibold text-gray-500 uppercase mb-1.5">Search in</p>
            <select
              value={filters.repoId}
              onChange={(e) => setFilters((f) => ({ ...f, repoId: e.target.value }))}
              className="w-full bg-white border border-gray-300 rounded-lg px-3 py-2 text-sm text-text outline-none"
              data-testid="filter-scope"
            >
              <option value="">All libraries</option>
              {repos.map((r) => (
                <option key={r.repo_id} value={r.repo_id}>{r.repo_name}</option>
              ))}
            </select>
          </div>

          {filtersActive(filters) && (
            <button
              type="button"
              onClick={() => setFilters(DEFAULT_FILTERS)}
              className="text-sm text-red-500"
              data-testid="filter-reset"
            >
              Reset filters
            </button>
          )}
        </div>
      )}

      {/* Recent searches */}
      {showRecent && (
        <div className="p-3" data-testid="recent-searches">
          <div className="flex items-center justify-between mb-2">
            <span className="text-sm font-medium text-gray-500">Recent Searches</span>
            <button
              onClick={clearAllRecent}
              className="text-xs text-red-500"
              data-testid="clear-all-recent"
            >
              Clear All
            </button>
          </div>
          <div className="flex flex-wrap gap-2">
            {recentSearches.map((s) => (
              <div
                key={s}
                className="flex items-center gap-1 bg-gray-100 rounded-full px-3 py-1.5 text-sm"
                data-testid="recent-search-chip"
              >
                <Clock className="w-3 h-3 text-gray-400" />
                <button
                  onClick={() => handleRecentClick(s)}
                  className="text-text"
                  data-testid="recent-search-text"
                >
                  {s}
                </button>
                <button
                  onClick={() => removeRecentSearch(s)}
                  className="ml-0.5 p-0.5"
                  data-testid="remove-recent"
                >
                  <X className="w-3 h-3 text-gray-400" />
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Loading state */}
      {loading && (
        <div className="flex items-center justify-center p-8" data-testid="loading">
          <span className="text-gray-500">Searching...</span>
        </div>
      )}

      {/* Empty state when query entered but no results */}
      {!loading && query.length >= MIN_QUERY_LENGTH && results.length === 0 && (
        <div className="flex flex-col items-center justify-center p-8 text-center" data-testid="no-results">
          <Search className="w-12 h-12 text-gray-300 mb-4" />
          <p className="text-gray-500">No results found for &ldquo;{query}&rdquo;</p>
        </div>
      )}

      {/* Results grouped by library */}
      {!loading && results.length > 0 && (
        <div className="flex-1 overflow-y-auto" data-testid="search-results">
          <p className="text-xs text-gray-400 px-3 pt-2">{total} result{total !== 1 ? 's' : ''}</p>
          {Object.entries(groupedResults).map(([repoName, items]) => (
            <div key={repoName} data-testid="result-group">
              <div className="px-3 py-2 bg-gray-50 border-b border-gray-100">
                <span className="text-xs font-semibold text-gray-500 uppercase">{repoName}</span>
              </div>
              {items.map((result, idx) => (
                <button
                  key={`${result.repo_id}-${result.path}-${idx}`}
                  onClick={() => handleResultClick(result)}
                  className="flex items-center gap-3 w-full px-3 py-3 border-b border-gray-100 text-left hover:bg-gray-50 active:bg-gray-100"
                  data-testid="search-result-item"
                >
                  <FileIcon type={result.is_dir ? 'folder' : getFileType(result.name)} size={24} />
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-text truncate">
                      <HighlightedText text={result.name} query={query} />
                    </p>
                    <p className="text-xs text-gray-400 truncate">{result.path}</p>
                  </div>
                </button>
              ))}
            </div>
          ))}

          {/* Infinite scroll sentinel */}
          {hasMore && (
            <div ref={sentinelRef} className="flex items-center justify-center p-4" data-testid="load-more-sentinel">
              {loadingMore && <span className="text-sm text-gray-400">Loading more...</span>}
            </div>
          )}
        </div>
      )}

      {/* Initial empty state */}
      {!loading && query.length < MIN_QUERY_LENGTH && recentSearches.length === 0 && (
        <div className="flex flex-col items-center justify-center p-8 text-center flex-1">
          <Search className="w-12 h-12 text-gray-300 mb-4" />
          <p className="text-gray-500">Search your files and libraries</p>
        </div>
      )}
    </div>
  );
}
