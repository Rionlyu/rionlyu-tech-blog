---
title: The acknowledgement is the durability boundary
slug: acknowledgement-durability-boundary
date: 2026-07-22
summary: A small HTTP delivery service is only as durable as the promise it makes before replying.
draft: false
---

An HTTP `202 Accepted` response looks modest. In a delivery system, it is a contract: the caller is now allowed to forget the request. Everything important follows from deciding exactly when the server may make that promise.

## The dangerous interval

Consider a process that receives a request, places it in memory, sends `202`, and writes it to disk a moment later. The happy path is quick. The failure path contains a gap:

1. the server accepts the bytes;
2. the server acknowledges them;
3. the process stops before persistence completes.

The caller saw success, but the delivery no longer exists. Retries cannot repair a request that both sides believe the other side owns.

The durable ordering is less exciting and more useful:

```text
validate → append → synchronize → acknowledge → deliver
```

That synchronization step is the boundary. Before it completes, the caller still owns the request. After it completes, the delivery service does.

## A small state machine beats a vague queue

Durability is not only about writing bytes. Recovery needs to produce the same logical state every time. An append-only journal can represent mutations such as enqueue, lease, success, retry, and cancellation. Startup replays those records into an in-memory view; the journal remains the authority.

This is the model used by [spoold](https://github.com/Rionlyu/spoold): it synchronizes a delivery to its local journal before acknowledging it, then performs delivery in the background. The design avoids requiring a separate database or broker, but it does not pretend to be a distributed queue.

## At-least-once is the honest result

There is another unavoidable interval on the outbound side. A destination may accept a request just before the delivery process stops, but before success is persisted locally. After recovery, the only safe action is to try again.

That means a durable local spool provides at-least-once delivery. Destinations need a stable delivery identifier and an idempotent way to handle duplicates. Calling this edge case “exactly once” would move the ambiguity into undocumented behavior; naming it makes the integration testable.

## Recovery is part of the write path

The persistence format should be designed from the replay function backward. A useful recovery contract answers concrete questions:

- What happens to a partial final record?
- What happens to corruption in an earlier record?
- Can an expired lease return to pending work?
- Can compaction replace the journal without creating a new acknowledgement gap?

If these answers are unclear, the system has a storage mechanism, not yet a durability model.

## Keep the boundary narrow

A local spool is useful when one process needs its outbound request to survive its own lifetime. It is not the right boundary when enqueueing must be atomic with an application database transaction, or when work must remain available across multiple hosts.

The important design move is not adding machinery. It is choosing one promise, putting the expensive operation immediately before that promise, and making every recovery path preserve it.
