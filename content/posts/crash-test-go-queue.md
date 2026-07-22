---
title: Crash-test a file-backed Go queue at failure boundaries
slug: crash-test-file-backed-go-queue
date: 2026-07-08
summary: Turn durability claims into a byte-level replay test and a process-crash matrix.
draft: false
---

The useful test for a durable queue is not “can it reopen its file?” Kill it at the worst possible moment, reopen it, and prove that every acknowledged item is still pending or completed. An item that vanished after acknowledgement is data loss. A delivery attempted twice is the expected cost of at-least-once delivery and must keep the same idempotency key.

That gives a testable contract before any test code exists:

- an acknowledged enqueue must survive recovery;
- an unacknowledged enqueue may be absent **or** present;
- a truncated final record may be discarded;
- any complete record with a bad checksum must fail recovery loudly;
- a delivery accepted remotely but not recorded locally must be retried.

The second rule matters. A process can die after persistence but before its caller receives success. Recovery is allowed to contain that item even though the caller retries it.

## Make one record independently verifiable

Use a framed journal rather than newline-delimited arbitrary bytes:

```text
4-byte payload length | payload | 4-byte CRC32
```

The length tells replay where the next record should begin. The checksum distinguishes a complete record from corrupt bytes that happen to have a plausible length. Reject lengths above a configured record limit before allocating memory. Put record type, version, delivery ID, and payload inside the checksummed payload so recovery validates them together.

Writing also has to handle short writes. `io.Writer.Write` may consume fewer bytes than requested, so a single successful call is not a complete-write guarantee:

```go
func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		p = p[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
```

Append the complete frame, synchronize it, and only then let the service acknowledge the enqueue:

```go
type crashHook func(point string)

func appendFrame(f *os.File, frame []byte, crash crashHook) error {
	if err := writeFull(f, frame); err != nil {
		return err
	}
	if crash != nil {
		crash("after-write")
	}

	if err := f.Sync(); err != nil {
		return err
	}
	if crash != nil {
		crash("after-sync")
	}
	return nil
}
```

[`os.File.Sync`](https://pkg.go.dev/os#File.Sync) is the Go operation that commits the file’s current contents to stable storage. Treat its error as an enqueue failure. The hook is nil in production and a deliberate exit point in tests.

## Test every possible torn tail

Process crashes are good integration tests but poor at choosing the exact byte where a write stops. Test that boundary deterministically by writing every prefix of a valid frame after an already valid journal:

```go
func TestReplayIgnoresEveryTruncatedFinalFrame(t *testing.T) {
	base := encode(enqueued("kept"))
	next := encode(enqueued("maybe"))

	for cut := 0; cut < len(next); cut++ {
		t.Run(strconv.Itoa(cut), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "queue.log")
			data := append(append([]byte(nil), base...), next[:cut]...)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}

			state, err := replay(path)
			if err != nil {
				t.Fatal(err)
			}
			if !state.Pending("kept") || state.Pending("maybe") {
				t.Fatalf("cut=%d produced %#v", cut, state)
			}
		})
	}
}
```

Add separate cases that flip one byte in the first complete record and in the middle of a multi-record file. Those cases must return corruption, not silently skip to a later record. Ignoring only an incomplete final frame is a recovery rule; ignoring damage anywhere is data loss with better manners.

## Kill a helper process at semantic boundaries

Run the queue in a subprocess so the test can terminate it without killing the test runner:

```go
func TestCrashWorker(t *testing.T) {
	if os.Getenv("QUEUE_CRASH_WORKER") != "1" {
		t.Skip("helper process")
	}

	q, err := Open(os.Getenv("QUEUE_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	q.crash = func(point string) {
		if point == os.Getenv("CRASH_AT") {
			os.Exit(99) // skips deferred cleanup
		}
	}
	if err := q.Enqueue("delivery-42", []byte("payload")); err != nil {
		t.Fatal(err)
	}
}
```

The parent runs only that helper test, expects exit code `99`, then opens the same directory and checks recovered state:

```go
cmd := exec.Command(os.Args[0], "-test.run=^TestCrashWorker$")
cmd.Env = append(os.Environ(),
	"QUEUE_CRASH_WORKER=1",
	"QUEUE_DIR="+dir,
	"CRASH_AT=after-sync",
)
err := cmd.Run()

var exitErr *exec.ExitError
if !errors.As(err, &exitErr) || exitErr.ExitCode() != 99 {
	t.Fatalf("worker did not crash at hook: %v", err)
}
recovered, err := Open(dir)
if err != nil {
	t.Fatal(err)
}
if !recovered.Pending("delivery-42") {
	t.Fatal("synchronized enqueue disappeared")
}
```

Repeat the parent test for each boundary in the real state machine. Do not require an item to be absent before acknowledgement; require it to be present after synchronization.

| Crash point | Required recovery result |
|---|---|
| partway through final frame | prior records intact; partial tail ignored |
| after append, before `Sync` | new item absent or present |
| after `Sync`, before acknowledgement | new item present; caller may retry |
| after acknowledgement | new item present |
| after remote acceptance, before completion `Sync` | item pending again; duplicate delivery possible |
| after completion `Sync` | item must not be delivered again |

Use the same stable delivery ID on every retry, and make the receiver’s operation idempotent. No local journal can atomically commit a remote HTTP response and a local completion record.

## Know what the test did not prove

`os.Exit` and `Process.Kill` test abrupt process termination, replay, and skipped cleanup. They do **not** clear the operating system’s page cache, so they do not prove survival after power loss. Run the suite on the filesystem used in production, keep `Sync` in the write protocol, and use VM or block-device fault injection when the power-loss claim matters.

Compaction needs its own crash matrix. On Linux, the conservative replacement protocol is: write a temporary file, synchronize it, rename it over the old snapshot, then synchronize the containing directory before deleting older data. [`fsync(2)`](https://man7.org/linux/man-pages/man2/fsync.2.html) explains why syncing a file does not necessarily persist its directory entry; [`rename(2)`](https://man7.org/linux/man-pages/man2/rename.2.html) provides atomic name replacement, not durability by itself.

This is the standard behind [spoold](https://github.com/Rionlyu/spoold): the durability claim is only as credible as the crashes the recovery test can survive.
