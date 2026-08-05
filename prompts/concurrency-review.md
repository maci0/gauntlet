You are a senior software engineer specializing in concurrent and parallel systems. Your task is to perform a deep concurrency audit of this codebase.

Your goal is to identify race conditions, deadlocks, data corruption risks, and correctness issues in concurrent code. Focus on bugs that are hard to reproduce, cause intermittent failures, or silently corrupt data under load.

Review the following:

1. Shared mutable state
- Variables, fields, or data structures accessed from multiple threads, goroutines, or async tasks without synchronization
- Global or module-level mutable state modified concurrently
- Instance fields mutated by concurrent method calls without protection
- Collections (maps, lists, sets) read and written concurrently without thread-safe variants or locks
- Lazy initialization patterns that are not thread-safe (check-then-act without atomics)
- Caches or memoization shared across threads without proper synchronization
- Counters, flags, or status fields updated without atomic operations
- Struct or object fields partially updated (torn reads/writes)

2. Race conditions
- Read-modify-write sequences without atomics or locks (counter++, balance += amount)
- Check-then-act patterns where the condition can change between check and action (TOCTOU)
- Double-checked locking implemented incorrectly
- Publication of objects before they are fully constructed
- Iterator invalidation from concurrent modification of the underlying collection
- File system operations that assume exclusive access without locking
- Database read-modify-write without optimistic or pessimistic locking
- Event handlers or callbacks that assume single-threaded execution

3. Deadlocks and livelocks
- Multiple locks acquired in inconsistent order across code paths
- Lock held while calling external code, callbacks, or virtual methods that may acquire other locks
- Blocking operations inside critical sections (I/O, network, database under lock)
- Reentrant lock acquisition where the lock is not reentrant
- Circular wait patterns between threads, services, or resources
- Starvation from unfair scheduling, priority inversion, or writer-biased locks
- Livelocks where threads repeatedly yield to each other without progress
- Missing lock timeout or deadlock detection

4. Async and await correctness
- Blocking calls inside async contexts (sync I/O in async function, .Result/.Wait in async path)
- Missing await on async calls (fire-and-forget without error handling)
- Async void or detached tasks that swallow exceptions
- Captured context issues (synchronization context, thread pool exhaustion)
- Missing cancellation token propagation through async chains
- Async methods that are not truly async (just wrapping sync code in Task.Run)
- Mixed sync and async code creating implicit thread pool dependencies
- Async lambdas or closures capturing mutable state from the enclosing scope

5. Thread and task lifecycle
- Threads or goroutines spawned without tracking or cleanup
- Missing join, wait, or completion tracking for background work
- Thread leaks from unhandled exceptions in worker threads
- Thread pool exhaustion from too many concurrent tasks or blocking operations
- Missing graceful shutdown that waits for in-flight work to complete
- Orphaned background tasks that continue after their parent is cancelled
- Daemon threads that silently die without error reporting
- Unbounded creation of threads, goroutines, or tasks without pooling or limits

6. Synchronization primitives
- Wrong primitive for the use case (mutex where rwlock would reduce contention, spinlock where mutex would avoid busy-waiting)
- Lock scope too broad (holding lock during I/O or expensive computation)
- Lock scope too narrow (releasing lock between related operations that need atomicity)
- Missing memory barriers or volatile annotations where required
- Condition variables used without proper predicate loops (spurious wakeups)
- Semaphore permits not released on error paths
- Missing unlock in error or exception paths (try/finally, defer)
- Lock-free algorithms implemented incorrectly (missing CAS loops, ABA problem)

7. Message passing and channels
- Unbounded channels or queues that grow without limit under load
- Missing backpressure when producers outpace consumers
- Channel sends that block indefinitely without timeout
- Missing poison pill or shutdown signal for worker loops
- Message ordering assumptions that do not hold under concurrent dispatch
- Missing error handling for failed message processing (message dropped silently)
- Shared mutable data passed through channels instead of owned values (aliasing)
- Fan-out patterns where slow consumers block fast producers

8. Concurrent data structures
- Standard (non-concurrent) collections used in concurrent contexts
- Concurrent collections used with compound operations that are not atomic (check-and-insert, get-and-modify)
- Missing use of concurrent-safe alternatives where available (ConcurrentHashMap, sync.Map)
- Lock granularity too coarse (single lock for the entire data structure when per-shard locking is possible)
- Iteration over concurrent collections without snapshot or copy
- Concurrent data structures with inconsistent views across operations
- Copy-on-write structures used in write-heavy workloads (wrong tradeoff)

9. Database and external service concurrency
- Missing transaction isolation level for concurrent access patterns
- Optimistic locking without retry on conflict
- Lost update patterns from concurrent read-modify-write across requests
- Missing row-level or advisory locking for exclusive operations
- Connection pool exhaustion from long-held connections or missing timeouts
- Distributed lock implementations without TTL or fencing tokens
- Cache stampede on expiration (thundering herd)
- Missing idempotency for operations that can be retried concurrently

10. Testing and verification
- Missing concurrent or stress tests for shared state
- Tests that pass by accident due to timing (sleep-based synchronization)
- Missing use of race detectors, thread sanitizers, or concurrency checkers
- Tests that only exercise the single-threaded path
- Missing property-based or fuzz testing for concurrent data structures
- Flaky tests that indicate underlying concurrency bugs
- Missing documentation of thread safety guarantees on public APIs
- Missing linting or static analysis for concurrency anti-patterns

Instructions:
- Trace shared state from declaration to all access points. Check every access for proper synchronization.
- Consider what happens under high concurrency, not just the single-threaded happy path.
- Think about timing: if two operations happen "at the same time", what can go wrong?
- Do not flag single-threaded code or code that is clearly only accessed from one thread.
- Focus on bugs that corrupt data or cause deadlocks, not on theoretical contention that reduces throughput.
- Consider the deployment context: single-process vs multi-process, single-node vs distributed.
- Concurrency bugs are hard to reproduce. If the code looks suspicious, flag it even if you cannot construct a definitive exploit.
- Distinguish between:
  - confirmed races (shared mutable state with no synchronization, provable interleaving)
  - likely races (patterns that typically cause races, missing synchronization on suspicious paths)
  - potential races (need more context about threading model or caller guarantees)
  - design issues (correct but fragile, hard to reason about, hard to maintain)
  - hardening opportunities (defense in depth, not urgent)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: file(s), symbol(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Failure scenario: what goes wrong under concurrent access
- Evidence from the code
- Recommendation
- Expected benefit: correctness / data integrity / reliability / debuggability
- Estimated effort

Output format:

## Executive Summary
- Overall concurrency safety assessment
- Critical data corruption or deadlock risks
- Top 3 highest impact fixes

## Data Races
Shared mutable state accessed without proper synchronization.

## Deadlock and Livelock Risks
Lock ordering issues, blocking under lock, circular waits.

## Async and Await Issues
Blocking in async contexts, missing awaits, fire-and-forget tasks.

## Lifecycle and Resource Issues
Thread leaks, missing shutdown, unbounded task creation.

## Synchronization Issues
Wrong primitives, missing unlocks, scope problems.

## Message Passing and Channel Issues
Unbounded queues, missing backpressure, ordering assumptions.

## Database and Distributed Concurrency
Missing locking, lost updates, cache stampede, connection exhaustion.

## Quick Wins
Small changes that eliminate high-risk concurrency bugs.

## Improvement Plan
- Ordered by data corruption risk:
  1. Fix confirmed data races (shared state without synchronization)
  2. Fix deadlock risks (inconsistent lock ordering, blocking under lock)
  3. Fix async correctness (blocking in async, missing awaits)
  4. Add missing lifecycle management (thread leaks, shutdown)
  5. Improve synchronization patterns (reduce contention, correct primitives)
  6. Add concurrency testing and race detection

## Thread Safety Inventory
- Public APIs and their thread safety guarantees (or lack thereof)
- Shared state and its synchronization mechanism
- Areas documented as single-threaded that may be called concurrently

## Open Questions
- Threading model assumptions that need team confirmation
- Areas where the concurrency design intent is unclear
- Questions about expected concurrency levels and access patterns

Important:
- Base findings on actual code: shared state, lock usage, async patterns, and threading model.
- If you are not sure about the threading model, state your assumption and flag it.
- Prefer the simplest correct fix: often a mutex is better than a lock-free algorithm.
- Do not recommend fine-grained locking optimizations unless contention is a proven problem.
- Eliminating shared mutable state is better than synchronizing it.
- A data race that corrupts user data is critical even if it rarely triggers.
- Call out when concurrent code is already well-synchronized and safe in specific areas.
