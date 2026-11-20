/*
 *  Copyright (c) 2026 Robert Beckebans. Part of trellis2.cpp (MIT).
 *
 *  Minimal stand-in for the sliver of Intel TBB that the vendored
 *  AutoRemesher core actually uses, implemented over <thread> so the project
 *  gains no TBB build dependency.  The upstream sources are NOT patched for
 *  this: `compat/tbb/*.h` provide the header names they include, and each of
 *  those pulls in this file.
 *
 *  Provided surface (verified against the vendored sources):
 *      tbb::blocked_range<T>        two-argument form only
 *      tbb::parallel_for(range, body)
 *      tbb::mutex                   included upstream, currently unused
 *      tbb::combinable<T>           included upstream, currently unused
 *
 *  Deliberate differences from real TBB:
 *    - No task stealing.  Chunks are handed out through one atomic counter,
 *      which is enough for the two shapes present here: a handful of very
 *      unevenly sized mesh islands, and large flat loops over vertices.
 *    - Nested parallel_for does not deadlock or oversubscribe: the inner call
 *      only spawns threads that the outer call left unclaimed.  AutoRemesher
 *      relies on this - resample() runs parallel loops inside the per-island
 *      parallel loop.
 *    - Exceptions escaping the body are captured and rethrown in the calling
 *      thread after the workers join.  Without this an exception would cross a
 *      std::thread boundary and call std::terminate.
 */
#ifndef T2_TBB_SHIM_H
#define T2_TBB_SHIM_H

#include <algorithm>
#include <atomic>
#include <cstddef>
#include <exception>
#include <mutex>
#include <thread>
#include <vector>

namespace tbb {

template <typename Value>
class blocked_range {
public:
    typedef Value const_iterator;
    typedef std::size_t size_type;

    blocked_range(Value begin, Value end)
        : m_begin(begin)
        , m_end(end)
    {
    }

    Value begin() const { return m_begin; }
    Value end() const { return m_end; }
    size_type size() const { return static_cast<size_type>(m_end - m_begin); }
    bool empty() const { return !(m_begin < m_end); }

private:
    Value m_begin;
    Value m_end;
};

typedef std::mutex mutex;

namespace detail {

    // Threads already handed out to enclosing parallel_for calls.  Kept global
    // rather than per-call so a nested loop cannot multiply the thread count.
    inline std::atomic<unsigned>& claimed_workers()
    {
        static std::atomic<unsigned> claimed(0);
        return claimed;
    }

    inline unsigned hardware_width()
    {
        static const unsigned width = std::max(1u, std::thread::hardware_concurrency());
        return width;
    }

} // namespace detail

// Split `range` into chunks and run `body` over each of them.  Body may be a
// lambda or a functor; both forms occur in the vendored sources.
template <typename Range, typename Body>
void parallel_for(const Range& range, const Body& body)
{
    typedef typename Range::const_iterator Value;

    const Value first = range.begin();
    const Value last = range.end();
    if (!(first < last))
        return;

    const std::size_t count = static_cast<std::size_t>(last - first);

    // Only claim threads the enclosing loops have not taken already.
    const unsigned width = detail::hardware_width();
    const unsigned taken = detail::claimed_workers().load(std::memory_order_relaxed);
    const unsigned spare = width > taken ? width - taken : 1u;
    const unsigned workers = static_cast<unsigned>(
        std::min<std::size_t>(spare, count));

    if (workers <= 1) {
        body(range);
        return;
    }

    // One item per chunk keeps wildly uneven work (mesh islands) balanced;
    // large uniform loops get bigger chunks so the counter is not the
    // bottleneck.
    const std::size_t chunk = std::max<std::size_t>(1, count / (workers * 8));

    std::atomic<std::size_t> next(0);
    std::exception_ptr failure;
    std::mutex failure_mutex;

    auto worker = [&]() {
        for (;;) {
            const std::size_t offset = next.fetch_add(chunk, std::memory_order_relaxed);
            if (offset >= count)
                break;
            const std::size_t end = std::min(count, offset + chunk);
            try {
                body(Range(first + static_cast<Value>(offset),
                    first + static_cast<Value>(end)));
            } catch (...) {
                std::lock_guard<std::mutex> lock(failure_mutex);
                if (!failure)
                    failure = std::current_exception();
                // Stop handing out work; the caller will rethrow.
                next.store(count, std::memory_order_relaxed);
                break;
            }
        }
    };

    const unsigned spawned = workers - 1; // the calling thread works too
    detail::claimed_workers().fetch_add(spawned, std::memory_order_relaxed);

    std::vector<std::thread> threads;
    threads.reserve(spawned);
    for (unsigned i = 0; i < spawned; ++i)
        threads.emplace_back(worker);

    worker();

    for (std::thread& thread : threads)
        thread.join();
    detail::claimed_workers().fetch_sub(spawned, std::memory_order_relaxed);

    if (failure)
        std::rethrow_exception(failure);
}

// Included by the vendored sources but currently unused by them.  Kept so the
// compat headers are not lying about what they provide.
template <typename T>
class combinable {
public:
    combinable() = default;

    T& local()
    {
        const std::thread::id self = std::this_thread::get_id();
        std::lock_guard<std::mutex> lock(m_mutex);
        for (auto& slot : m_slots) {
            if (slot.first == self)
                return slot.second;
        }
        m_slots.emplace_back(self, T());
        return m_slots.back().second;
    }

    template <typename Func>
    void combine_each(Func func)
    {
        std::lock_guard<std::mutex> lock(m_mutex);
        for (auto& slot : m_slots)
            func(slot.second);
    }

    void clear()
    {
        std::lock_guard<std::mutex> lock(m_mutex);
        m_slots.clear();
    }

private:
    std::vector<std::pair<std::thread::id, T>> m_slots;
    std::mutex m_mutex;
};

} // namespace tbb

#endif
