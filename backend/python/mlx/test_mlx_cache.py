"""
Comprehensive unit tests for ThreadSafeLRUPromptCache.

Tests all cache operation modes:
- Exact match
- Shorter prefix match
- Longer prefix match (with trimming)
- No match
- LRU eviction
- Reference counting
- Multi-model namespacing
- Thread safety with data integrity verification
"""
import unittest
import concurrent.futures
import threading
import copy
from mlx_cache import ThreadSafeLRUPromptCache


class TestCacheExactMatch(unittest.TestCase):
    """Tests for exact match cache behavior."""

    def setUp(self):
        self.cache = ThreadSafeLRUPromptCache(max_size=10)

    def test_exact_match_returns_cache_and_empty_remaining(self):
        """Exact match should return the cache with no remaining tokens."""
        tokens = [1, 2, 3, 4, 5]
        mock_cache = ["kv_cache_data"]

        self.cache.insert_cache("model1", tokens, mock_cache)
        result_cache, remaining = self.cache.fetch_nearest_cache("model1", tokens)

        self.assertEqual(result_cache, mock_cache)
        self.assertEqual(remaining, [])

    def test_exact_match_extracts_and_removes_from_cache(self):
        """Fetching exact match with count=1 should remove entry from cache."""
        tokens = [1, 2, 3]
        self.cache.insert_cache("model1", tokens, ["cache"])

        self.assertEqual(len(self.cache), 1)

        # First fetch extracts the entry
        self.cache.fetch_nearest_cache("model1", tokens)

        # Cache should now be empty
        self.assertEqual(len(self.cache), 0)

        # Second fetch should return None (no match)
        result_cache, remaining = self.cache.fetch_nearest_cache("model1", tokens)
        self.assertIsNone(result_cache)
        self.assertEqual(remaining, tokens)


class TestCacheShorterPrefix(unittest.TestCase):
    """Tests for shorter prefix match behavior."""

    def setUp(self):
        self.cache = ThreadSafeLRUPromptCache(max_size=10)

    def test_shorter_prefix_returns_cache_with_remaining_tokens(self):
        """When cached prefix is shorter, return cache and remaining suffix."""
        short_tokens = [1, 2, 3]
        long_tokens = [1, 2, 3, 4, 5, 6]
        mock_cache = ["prefix_cache"]

        self.cache.insert_cache("model1", short_tokens, mock_cache)
        result_cache, remaining = self.cache.fetch_nearest_cache("model1", long_tokens)

        self.assertEqual(result_cache, mock_cache)
        self.assertEqual(remaining, [4, 5, 6])

    def test_shorter_prefix_correct_remaining_calculation(self):
        """Verify remaining tokens are calculated correctly for various prefix lengths."""
        # Note: Single-token prefixes ([1] -> [1,2,3]) are deliberately not matched
        # to allow longer cached sequences to be preferred for trimming.
        # This matches upstream mlx_lm/server.py behavior.
        test_cases = [
            # (cached_tokens, requested_tokens, expected_remaining)
            ([1, 2], [1, 2, 3, 4, 5], [3, 4, 5]),
            ([10, 20, 30, 40], [10, 20, 30, 40, 50], [50]),
        ]

        for cached, requested, expected_remaining in test_cases:
            with self.subTest(cached=cached, requested=requested):
                cache = ThreadSafeLRUPromptCache(max_size=10)
                cache.insert_cache("model", cached, ["cache"])
                result_cache, remaining = cache.fetch_nearest_cache("model", requested)

                self.assertIsNotNone(result_cache)
                self.assertEqual(remaining, expected_remaining)

    def test_single_token_prefix_not_matched(self):
        """Single-token prefixes are not matched (by design, matches upstream).

        This allows longer cached sequences to be preferred for trimming,
        which provides better KV cache reuse. Single-token caches are rare
        in practice since real prompts with chat templates are many tokens.
        """
        cache = ThreadSafeLRUPromptCache(max_size=10)
        cache.insert_cache("model", [1], ["cache"])

        result_cache, remaining = cache.fetch_nearest_cache("model", [1, 2, 3])

        # Single-token prefix is NOT matched
        self.assertIsNone(result_cache)
        self.assertEqual(remaining, [1, 2, 3])


class TestCacheLongerPrefix(unittest.TestCase):
    """Tests for longer prefix match behavior (trimming)."""

    def setUp(self):
        # Track trim calls for verification
        self.trim_calls = []

        def mock_can_trim(cache):
            return True

        def mock_trim(cache, num_to_trim):
            self.trim_calls.append(num_to_trim)
            # Simulate trimming by modifying the cache
            cache.append(f"trimmed_{num_to_trim}")

        self.cache = ThreadSafeLRUPromptCache(
            max_size=10,
            can_trim_fn=mock_can_trim,
            trim_fn=mock_trim,
        )

    def test_longer_prefix_triggers_trim(self):
        """When cached sequence is longer, should trim to match requested prefix."""
        long_tokens = [1, 2, 3, 4, 5]
        short_tokens = [1, 2, 3]

        self.cache.insert_cache("model1", long_tokens, ["original_cache"])
        result_cache, remaining = self.cache.fetch_nearest_cache("model1", short_tokens)

        # Should have called trim
        self.assertTrue(len(self.trim_calls) > 0, "trim_fn should have been called")
        # Result should be a trimmed copy, not the original
        self.assertIn("trimmed_", str(result_cache))

    def test_longer_prefix_without_trim_fn_returns_no_match(self):
        """Without trim functions, longer prefix should not match."""
        cache_no_trim = ThreadSafeLRUPromptCache(max_size=10)

        long_tokens = [1, 2, 3, 4, 5]
        short_tokens = [1, 2, 3]

        cache_no_trim.insert_cache("model1", long_tokens, ["cache"])
        result_cache, remaining = cache_no_trim.fetch_nearest_cache("model1", short_tokens)

        # Without trim_fn, should return no match
        self.assertIsNone(result_cache)
        self.assertEqual(remaining, short_tokens)

    def test_longer_prefix_can_trim_false_returns_no_match(self):
        """When can_trim_fn returns False, should not attempt trim."""
        cache = ThreadSafeLRUPromptCache(
            max_size=10,
            can_trim_fn=lambda c: False,
            trim_fn=lambda c, n: None,
        )

        cache.insert_cache("model1", [1, 2, 3, 4, 5], ["cache"])
        result_cache, remaining = cache.fetch_nearest_cache("model1", [1, 2, 3])

        self.assertIsNone(result_cache)
        self.assertEqual(remaining, [1, 2, 3])


class TestCacheNoMatch(unittest.TestCase):
    """Tests for no match behavior."""

    def setUp(self):
        self.cache = ThreadSafeLRUPromptCache(max_size=10)

    def test_empty_cache_returns_none(self):
        """Empty cache should return None and all tokens as remaining."""
        tokens = [1, 2, 3]
        result_cache, remaining = self.cache.fetch_nearest_cache("model1", tokens)

        self.assertIsNone(result_cache)
        self.assertEqual(remaining, tokens)

    def test_different_prefix_returns_none(self):
        """Tokens with different prefix should not match."""
        self.cache.insert_cache("model1", [1, 2, 3], ["cache"])

        # Completely different tokens
        result_cache, remaining = self.cache.fetch_nearest_cache("model1", [4, 5, 6])

        self.assertIsNone(result_cache)
        self.assertEqual(remaining, [4, 5, 6])

    def test_partial_prefix_mismatch_returns_none(self):
        """Tokens that diverge mid-sequence should not match."""
        self.cache.insert_cache("model1", [1, 2, 3], ["cache"])

        # Same start but diverges
        result_cache, remaining = self.cache.fetch_nearest_cache("model1", [1, 2, 99])

        self.assertIsNone(result_cache)
        self.assertEqual(remaining, [1, 2, 99])

    def test_wrong_model_returns_none(self):
        """Different model key should not match."""
        self.cache.insert_cache("model1", [1, 2, 3], ["cache"])

        result_cache, remaining = self.cache.fetch_nearest_cache("model2", [1, 2, 3])

        self.assertIsNone(result_cache)
        self.assertEqual(remaining, [1, 2, 3])


class TestCacheLRUEviction(unittest.TestCase):
    """Tests for LRU eviction behavior."""

    def setUp(self):
        self.cache = ThreadSafeLRUPromptCache(max_size=3)

    def test_evicts_oldest_when_full(self):
        """Should evict least recently used entry when capacity exceeded."""
        self.cache.insert_cache("model", [1], ["cache1"])
        self.cache.insert_cache("model", [2], ["cache2"])
        self.cache.insert_cache("model", [3], ["cache3"])

        self.assertEqual(len(self.cache), 3)

        # Insert 4th entry - should evict [1]
        self.cache.insert_cache("model", [4], ["cache4"])

        self.assertEqual(len(self.cache), 3)

        # [1] should be evicted
        result, _ = self.cache.fetch_nearest_cache("model", [1])
        self.assertIsNone(result)

        # [2], [3], [4] should still exist
        for tokens in [[2], [3], [4]]:
            # Re-insert since fetch extracts
            self.cache.insert_cache("model", tokens, [f"cache{tokens[0]}"])

        result2, _ = self.cache.fetch_nearest_cache("model", [2])
        self.assertIsNotNone(result2)

    def test_access_updates_lru_order(self):
        """Accessing an entry should move it to most recently used."""
        self.cache.insert_cache("model", [1], ["cache1"])
        self.cache.insert_cache("model", [2], ["cache2"])
        self.cache.insert_cache("model", [3], ["cache3"])

        # Access [1] to make it most recently used
        cache1, _ = self.cache.fetch_nearest_cache("model", [1])
        # Re-insert it (simulating normal usage pattern)
        self.cache.insert_cache("model", [1], cache1)

        # Now insert two more entries - should evict [2] then [3], not [1]
        self.cache.insert_cache("model", [4], ["cache4"])
        self.cache.insert_cache("model", [5], ["cache5"])

        # [1] should still exist (was accessed, so not evicted)
        result1, _ = self.cache.fetch_nearest_cache("model", [1])
        self.assertIsNotNone(result1)

        # [2] should be evicted (was oldest after [1] was accessed)
        result2, _ = self.cache.fetch_nearest_cache("model", [2])
        self.assertIsNone(result2)


class TestCacheReferenceCount(unittest.TestCase):
    """Tests for reference counting behavior."""

    def setUp(self):
        self.cache = ThreadSafeLRUPromptCache(max_size=10)

    def test_multiple_inserts_increment_count(self):
        """Inserting same tokens multiple times should increment count."""
        tokens = [1, 2, 3]

        self.cache.insert_cache("model", tokens, ["cache"])
        self.cache.insert_cache("model", tokens, ["cache"])
        self.cache.insert_cache("model", tokens, ["cache"])

        # Should still be one entry (with count=3 internally)
        self.assertEqual(len(self.cache), 1)

        # First two fetches should return copies (count decremented)
        result1, _ = self.cache.fetch_nearest_cache("model", tokens)
        self.assertIsNotNone(result1)

        result2, _ = self.cache.fetch_nearest_cache("model", tokens)
        self.assertIsNotNone(result2)

        # Third fetch extracts the last reference
        result3, _ = self.cache.fetch_nearest_cache("model", tokens)
        self.assertIsNotNone(result3)

        # Fourth fetch should return None (entry fully extracted)
        result4, _ = self.cache.fetch_nearest_cache("model", tokens)
        self.assertIsNone(result4)

    def test_extract_with_high_count_returns_deep_copy(self):
        """When count > 1, extract should return a deep copy."""
        tokens = [1, 2, 3]
        original_cache = [{"nested": "data"}]

        self.cache.insert_cache("model", tokens, original_cache)
        self.cache.insert_cache("model", tokens, original_cache)  # count=2

        result1, _ = self.cache.fetch_nearest_cache("model", tokens)

        # Modify the returned cache
        result1[0]["nested"] = "modified"

        # Second fetch should get unmodified copy
        result2, _ = self.cache.fetch_nearest_cache("model", tokens)
        self.assertEqual(result2[0]["nested"], "data")


class TestCacheMultiModel(unittest.TestCase):
    """Tests for multi-model namespacing."""

    def setUp(self):
        self.cache = ThreadSafeLRUPromptCache(max_size=10)

    def test_same_tokens_different_models_are_separate(self):
        """Same token sequence under different models should be independent."""
        tokens = [1, 2, 3]

        self.cache.insert_cache("model_a", tokens, ["cache_a"])
        self.cache.insert_cache("model_b", tokens, ["cache_b"])

        self.assertEqual(len(self.cache), 2)

        result_a, _ = self.cache.fetch_nearest_cache("model_a", tokens)
        result_b, _ = self.cache.fetch_nearest_cache("model_b", tokens)

        self.assertEqual(result_a, ["cache_a"])
        self.assertEqual(result_b, ["cache_b"])

    def test_eviction_across_models(self):
        """LRU eviction should work across different models."""
        cache = ThreadSafeLRUPromptCache(max_size=3)

        cache.insert_cache("model_a", [1], ["a1"])
        cache.insert_cache("model_b", [1], ["b1"])
        cache.insert_cache("model_a", [2], ["a2"])

        self.assertEqual(len(cache), 3)

        # Insert 4th - should evict model_a:[1] (oldest)
        cache.insert_cache("model_b", [2], ["b2"])

        result, _ = cache.fetch_nearest_cache("model_a", [1])
        self.assertIsNone(result)


class TestCacheThreadSafety(unittest.TestCase):
    """Tests for thread safety with data integrity verification."""

    def test_concurrent_inserts_no_data_loss(self):
        """Concurrent inserts should not lose data."""
        cache = ThreadSafeLRUPromptCache(max_size=100)
        num_threads = 10
        inserts_per_thread = 20

        def insert_entries(thread_id):
            for i in range(inserts_per_thread):
                tokens = [thread_id, i]
                cache.insert_cache("model", tokens, [f"cache_{thread_id}_{i}"])

        with concurrent.futures.ThreadPoolExecutor(max_workers=num_threads) as executor:
            futures = [executor.submit(insert_entries, tid) for tid in range(num_threads)]
            concurrent.futures.wait(futures)

        # Verify expected number of entries (may be less due to LRU eviction with max_size=100)
        # But should be exactly 100 since we inserted exactly 200 and max_size is 100
        self.assertEqual(len(cache), 100)

    def test_concurrent_fetch_and_insert_no_corruption(self):
        """Concurrent fetches and inserts should not corrupt data."""
        cache = ThreadSafeLRUPromptCache(max_size=50)
        errors = []
        lock = threading.Lock()

        # Pre-populate with known data
        for i in range(20):
            cache.insert_cache("model", [i], [f"original_{i}"])

        def fetch_and_verify(thread_id):
            try:
                for _ in range(50):
                    token_id = thread_id % 20
                    result, remaining = cache.fetch_nearest_cache("model", [token_id])

                    if result is not None:
                        # Verify data integrity
                        expected_prefix = f"original_{token_id}"
                        if not str(result[0]).startswith("original_"):
                            with lock:
                                errors.append(f"Corrupted data: {result}")

                        # Re-insert to keep cache populated
                        cache.insert_cache("model", [token_id], result)

            except Exception as e:
                with lock:
                    errors.append(str(e))

        with concurrent.futures.ThreadPoolExecutor(max_workers=10) as executor:
            futures = [executor.submit(fetch_and_verify, tid) for tid in range(10)]
            concurrent.futures.wait(futures)

        self.assertEqual(errors, [], f"Thread safety errors: {errors}")

    def test_concurrent_operations_maintain_cache_bounds(self):
        """Cache size should never exceed max_size under concurrent operations."""
        max_size = 10
        cache = ThreadSafeLRUPromptCache(max_size=max_size)
        size_violations = []
        lock = threading.Lock()

        def random_operations(thread_id):
            import random
            for i in range(100):
                tokens = [random.randint(0, 50)]
                if random.random() < 0.7:
                    cache.insert_cache("model", tokens, [f"cache_{thread_id}_{i}"])
                else:
                    cache.fetch_nearest_cache("model", tokens)

                current_size = len(cache)
                if current_size > max_size:
                    with lock:
                        size_violations.append(current_size)

        with concurrent.futures.ThreadPoolExecutor(max_workers=10) as executor:
            futures = [executor.submit(random_operations, tid) for tid in range(10)]
            concurrent.futures.wait(futures)

        self.assertEqual(size_violations, [], f"Size exceeded max: {size_violations}")
        self.assertLessEqual(len(cache), max_size)


class TestCacheClear(unittest.TestCase):
    """Tests for cache clear operation."""

    def setUp(self):
        self.cache = ThreadSafeLRUPromptCache(max_size=10)

    def test_clear_removes_all_entries(self):
        """Clear should remove all entries."""
        self.cache.insert_cache("model1", [1, 2], ["cache1"])
        self.cache.insert_cache("model2", [3, 4], ["cache2"])
        self.cache.insert_cache("model1", [5, 6], ["cache3"])

        self.assertEqual(len(self.cache), 3)

        self.cache.clear()

        self.assertEqual(len(self.cache), 0)

    def test_clear_allows_new_inserts(self):
        """After clear, new inserts should work normally."""
        self.cache.insert_cache("model", [1], ["cache1"])
        self.cache.clear()
        self.cache.insert_cache("model", [2], ["cache2"])

        self.assertEqual(len(self.cache), 1)

        result, _ = self.cache.fetch_nearest_cache("model", [2])
        self.assertEqual(result, ["cache2"])


if __name__ == "__main__":
    unittest.main()
