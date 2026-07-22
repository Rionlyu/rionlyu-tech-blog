---
title: Debug a Kubernetes Job from queue to container startup
slug: debug-kubernetes-job-never-starts
date: 2026-07-15
summary: A read-only workflow for finding the exact stage where a Job stopped making progress.
draft: false
---

When a Kubernetes Job never runs, `kubectl logs` is usually the wrong first command. Logs only become useful after a container has started. First find the last transition the workload completed:

```text
Job created → queue admitted (if used) → Pod created → scheduled → container started → Job completed
```

The failure is immediately after the last transition you can prove. The workflow below is read-only, so it is safe to use before anyone starts deleting Pods or changing resource requests.

## Take one snapshot

Set the namespace and Job once, then collect the three views that answer most first-pass questions:

```sh
ns=payments
job=nightly-reconcile

kubectl -n "$ns" get job "$job" -o wide
kubectl -n "$ns" describe job "$job"
kubectl -n "$ns" get pods \
  -l "batch.kubernetes.io/job-name=$job" -o wide
```

Run them close together. Kubernetes objects keep changing, and comparing output captured ten minutes apart can produce a diagnosis that was never true at one point in time.

Now choose the branch that matches the evidence:

| What exists? | Inspect next | Failure area |
|---|---|---|
| Job, but zero Pods | suspension, parallelism, Job events, queue admission | controller or admission |
| Pending Pod with no node | Pod scheduling events | scheduler |
| Pending Pod with a node | init containers and waiting states | startup |
| Running or restarted container | current and previous logs | application |
| Failed Job | Job conditions and retry policy | controller policy |

## Branch 1: the Job has created no Pods

Check whether the Job is allowed to create work:

```sh
kubectl -n "$ns" get job "$job" \
  -o jsonpath='{.spec.suspend}{"\t"}{.spec.parallelism}{"\n"}'
```

`suspend: true` tells the Job controller not to run Pods. A parallelism value of `0` also pauses execution. If neither explains the result, read the Events at the bottom of `kubectl describe job`: the controller can fail to create a Pod because of quota, admission policy, or another rejected API request. The [Kubernetes Job documentation](https://kubernetes.io/docs/concepts/workloads/controllers/job/) describes both suspension and the controller failure paths.

If the cluster uses Kueue, a suspended Job may simply be waiting for admission. Find the Workload whose owner is this Job, then inspect its conditions:

```sh
kubectl -n "$ns" get workloads.kueue.x-k8s.io \
  -o custom-columns='NAME:.metadata.name,OWNER:.metadata.ownerReferences[0].name,QUEUE:.spec.queueName'

kubectl -n "$ns" describe workload <workload-name>
```

Look at `QuotaReserved`, `Admitted`, `PodsReady`, and any eviction or requeue message. No reservation points toward unavailable ClusterQueue quota. A reservation without admission points toward admission checks. An admitted workload that was later suspended may have been preempted or requeued after its Pods failed to become ready. Kueue’s [Job troubleshooting guide](https://kueue.sigs.k8s.io/docs/tasks/troubleshooting/troubleshooting_jobs/) starts from the same Workload status because the Job alone cannot explain queue state.

## Branch 2: a Pod is Pending

Copy the Pod name from the first snapshot:

```sh
pod=nightly-reconcile-abcde

kubectl -n "$ns" get pod "$pod" \
  -o jsonpath='{.spec.nodeName}{"\n"}'
kubectl -n "$ns" describe pod "$pod"
```

An empty node name means scheduling has not completed. Read the latest `FailedScheduling` event rather than guessing. It normally names the blocking constraint: insufficient requested CPU or memory, an unmatched node selector or affinity rule, an untolerated taint, or an unbound volume claim. Compare that message with the Pod spec; current node capacity by itself does not explain what the scheduler evaluated.

If a node is present, scheduling succeeded. The Pod is stuck during startup instead. Inspect init-container and application-container states:

```sh
kubectl -n "$ns" get pod "$pod" -o jsonpath='\
{range .status.initContainerStatuses[*]}init/{.name}{"\t"}{.state.waiting.reason}{"\t"}{.state.terminated.reason}{"\n"}{end}\
{range .status.containerStatuses[*]}{.name}{"\t"}{.state.waiting.reason}{"\t"}{.state.terminated.reason}{"\t"}{.restartCount}{"\n"}{end}'
```

Reasons such as `ErrImagePull`, `ImagePullBackOff`, and `CreateContainerConfigError` move the investigation to the image reference, registry credentials, ConfigMaps, or Secrets named by the Pod. A failing init container must complete before an application container can start, so inspect it first.

## Branch 3: a container started

Only now are logs the right evidence:

```sh
kubectl -n "$ns" logs "job/$job" \
  --all-pods=true --all-containers=true --prefix

kubectl -n "$ns" logs "$pod" \
  --all-containers=true --previous --prefix
```

The first command reads every current Pod and container selected for the Job. The second asks for the previous container instances in a particular Pod, which is useful after a restart and returns nothing when no previous instance exists. These flags and resource forms are documented in [`kubectl logs`](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_logs/).

If the Job has already become `Failed`, do not stop at the application’s exit code. Check `backoffLimit`, `activeDeadlineSeconds`, `podFailurePolicy`, and the Job conditions. They explain why the controller retried, stopped retrying, or classified a Pod failure in a particular way.

## Finish with one evidenced sentence

A useful incident note names the blocked transition and cites the object that proves it:

> The Job has no Pods because its Kueue Workload is not admitted; `QuotaReserved=False` reports that the ClusterQueue has no available CPU quota.

That sentence is much easier to review than “Kubernetes is stuck.” Preserve the Job, Pod, and Workload descriptions before making a change, and redact sensitive environment values before attaching them to an issue. If doing this repeatedly is tedious, [kube-job-doctor](https://github.com/Rionlyu/kube-job-doctor) automates the same evidence-first, read-only approach.
