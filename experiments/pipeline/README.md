# Bounded fanout experiment

An in-memory scheduler prototype. `NewBlock` makes one owned immutable input;
`Broadcast` offers it independently to fixed, serial consumer workers. Each
queue has a finite block count. An accepted offer and a successful processing
cursor are deliberately separate. A saturated worker returns backpressure
without waiting for it, so another worker can advance. The caller must retain or
reacquire and retry rejected heights for that worker in order. A failed worker
may have accepted additional queued blocks that were never processed; restart
from the successful cursor, not the accepted cursor. Stop and callback failures
affect only that worker. Cancellation is cooperative.

This does **not** fetch, verify or decode Bitcoin blocks, persist checkpoints,
guarantee durable atomic commits, handle chain reorganizations, supply dependency
ordering, implement retry scheduling, or impose a byte budget. Queues bound
block count, not total memory; the caller must bound block sizes, the number of
consumers, and retained input. `Block.Bytes` copies on access for immutability;
it is not a shared-decoding performance claim. All cursors are volatile, and a
callback can perform side effects before an error or cancellation. An actual
engine needs transactional recovery and independent committed coverage.

Run `go test ./...` from this directory; `go test -race ./...` additionally
requires a supported C compiler and CGO enabled on Windows. The tests exercise
bounded backpressure, independent advancement, per-consumer ordering, failed
work and cooperative cancellation. They do not constitute real-block benchmarks.
