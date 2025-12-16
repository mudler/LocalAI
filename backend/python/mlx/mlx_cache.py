"""
Thread-safe LRU prompt cache for MLX-based backends.

Ported from mlx_lm/server.py (MIT License, Copyright 2023-2024 Apple Inc.)
with thread-safety additions for LocalAI's gRPC backend.

Usage:
    from mlx_cache import ThreadSafeLRUPromptCache

    # In LoadModel:
    self.lru_cache = ThreadSafeLRUPromptCache(max_size=10)

    # In Predict/PredictStream:
    prompt_cache, remaining_tokens = self.lru_cache.fetch_nearest_cache(model_key, tokens)
    # ... generate ...
    self.lru_cache.insert_cache(model_key, tokens, prompt_cache)
"""
import copy
import threading
from collections import deque
from dataclasses import dataclass
from typing import Any, List, Optional, Tuple


@dataclass
class CacheEntry:
    """A cache entry with reference counting."""
    prompt_cache: List[Any]
    count: int


@dataclass
class SearchResult:
    """Result of searching the cache trie."""
    model: Any
    exact: Optional[List[int]]
    shorter: Optional[List[int]]
    longer: Optional[List[int]]
    common_prefix: int


class ThreadSafeLRUPromptCache:
    """
    Thread-safe LRU cache with prefix matching for prompt KV caches.

    This cache stores KV caches keyed by token sequences and supports:
    - Exact match: Return the cache for the exact token sequence
    - Shorter prefix match: Return a cache for a prefix of the tokens
    - Longer prefix match: If a longer sequence is cached and can be trimmed
    - LRU eviction: When max_size is exceeded, evict least recently used

    Thread safety is provided via a threading.Lock that protects all
    cache operations.

    Args:
        max_size: Maximum number of cache entries (default: 10)
        can_trim_fn: Optional function to check if a cache can be trimmed
        trim_fn: Optional function to trim a cache
    """

    def __init__(
        self,
        max_size: int = 10,
        can_trim_fn: Optional[Any] = None,
        trim_fn: Optional[Any] = None,
    ):
        self.max_size = max_size
        self._cache = {}
        self._lru = deque()
        self._lock = threading.Lock()

        # Optional trim functions (for longer prefix reuse)
        self._can_trim_fn = can_trim_fn
        self._trim_fn = trim_fn

    def _search(self, model, tokens: List[int]) -> SearchResult:
        """
        Search the cache for a prompt cache. Return exact or close match.

        The cache is organized as a trie where each node is keyed by a token.
        This allows efficient prefix matching.
        """
        if model not in self._cache:
            return SearchResult(model, None, None, None, 0)

        current = self._cache[model]
        last_cache_index = -1
        index = 0

        # Traverse the trie following the token sequence
        while index < len(tokens) and tokens[index] in current:
            current = current[tokens[index]]
            if "cache" in current:
                last_cache_index = index
            index += 1

        # Exact match - no need to search for longer or shorter caches
        if last_cache_index == len(tokens) - 1:
            return SearchResult(model, tuple(tokens), None, None, 0)

        # Find the shorter cache (a prefix that has a cache)
        # Note: Uses > 0 (not >= 0) to match upstream mlx_lm/server.py behavior.
        # Single-token prefixes are not matched, which allows longer cached
        # sequences to be preferred for trimming. This is acceptable because
        # real prompts with chat templates are always many tokens.
        shorter = None
        if last_cache_index > 0:
            shorter = tuple(tokens[: last_cache_index + 1])

        # Check for caches that are longer than our token sequence
        longer = None
        common_prefix = index
        if index > 0 and last_cache_index <= 0:
            best = None
            stack = [(current, [])]
            while stack:
                current, extra = stack.pop()
                if "cache" in current:
                    if best is None or len(extra) < len(best):
                        best = extra
                else:
                    for tok in current:
                        stack.append((current[tok], extra + [tok]))
            if best is not None:
                longer = tuple(tokens[:index] + best)

        return SearchResult(model, None, shorter, longer, common_prefix)

    def _get(self, model, tokens: Tuple[int, ...]) -> CacheEntry:
        """Get a cache entry by traversing the trie."""
        current = self._cache[model]
        for tok in tokens:
            current = current[tok]
        return current["cache"]

    def _delete(self, model, tokens: Tuple[int, ...]) -> None:
        """Delete a cache entry and clean up empty trie nodes."""
        path = [self._cache[model]]
        for tok in tokens:
            path.append(path[-1][tok])
        del path[-1]["cache"]

        # Clean up empty nodes bottom-up
        for i in reversed(range(len(tokens))):
            d_prev, d, t = path[i], path[i + 1], tokens[i]
            if len(d) > 0:
                break
            del d_prev[t]

    def _extract(self, model, tokens: Tuple[int, ...]) -> CacheEntry:
        """
        Extract a cache entry for exclusive use.

        If the entry has count > 1, deep copy and decrement.
        If count == 1, remove from cache entirely.
        """
        cache_entry = self._get(model, tokens)
        if cache_entry.count == 1:
            self._delete(model, tokens)
            self._lru.remove((model, tokens))
            return cache_entry

        cache_entry.count -= 1
        return CacheEntry(
            copy.deepcopy(cache_entry.prompt_cache),
            1,
        )

    def fetch_nearest_cache(
        self, model, tokens: List[int]
    ) -> Tuple[Optional[List[Any]], List[int]]:
        """
        Fetch the nearest cache for the given token sequence.

        Thread-safe. Returns (cache, remaining_tokens) where:
        - cache: The KV cache to use (or None if no cache found)
        - remaining_tokens: Tokens that still need to be processed

        Args:
            model: Model identifier (used to namespace caches)
            tokens: The full token sequence for the prompt

        Returns:
            Tuple of (prompt_cache, remaining_tokens)
        """
        with self._lock:
            tokens_tuple = tuple(tokens)
            result = self._search(model, tokens)

            # Exact match - extract and return
            if result.exact is not None:
                cache_entry = self._extract(result.model, result.exact)
                return cache_entry.prompt_cache, []

            # Shorter prefix match - extract and return remaining
            if result.shorter is not None:
                cache_entry = self._extract(result.model, result.shorter)
                prefix_len = len(result.shorter)
                return cache_entry.prompt_cache, list(tokens[prefix_len:])

            # Longer prefix match - try to trim if possible
            if result.longer is not None and self._can_trim_fn is not None:
                cache_entry = self._get(result.model, result.longer)
                if self._can_trim_fn(cache_entry.prompt_cache):
                    # Deep copy and trim
                    trimmed_cache = copy.deepcopy(cache_entry.prompt_cache)
                    prefix = min(len(tokens) - 1, result.common_prefix)
                    num_to_trim = len(result.longer) - prefix
                    if self._trim_fn is not None:
                        self._trim_fn(trimmed_cache, num_to_trim)
                    return trimmed_cache, list(tokens[prefix:])

            # No match found
            return None, list(tokens)

    def insert_cache(
        self, model, tokens: List[int], prompt_cache: List[Any]
    ) -> None:
        """
        Insert a cache entry after generation completes.

        Thread-safe. Handles LRU eviction if max_size is exceeded.

        Args:
            model: Model identifier (used to namespace caches)
            tokens: The full token sequence (prompt + generated)
            prompt_cache: The KV cache to store
        """
        with self._lock:
            tokens_tuple = tuple(tokens)

            if model not in self._cache:
                self._cache[model] = {}
            current = self._cache[model]

            # Build trie path
            for tok in tokens_tuple:
                if tok not in current:
                    current[tok] = {}
                current = current[tok]

            # Update or create entry
            if "cache" in current:
                current["cache"].count += 1
                self._lru.remove((model, tokens_tuple))
            else:
                current["cache"] = CacheEntry(prompt_cache, 1)

            # Update LRU order
            self._lru.append((model, tokens_tuple))

            # Evict if over capacity
            if len(self._lru) > self.max_size:
                evict_model, evict_tokens = self._lru.popleft()
                self._delete(evict_model, evict_tokens)

    def clear(self) -> None:
        """Clear all cache entries. Thread-safe."""
        with self._lock:
            self._cache.clear()
            self._lru.clear()

    def __len__(self) -> int:
        """Return the number of cache entries. Thread-safe."""
        with self._lock:
            return len(self._lru)
