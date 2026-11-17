# Consecutive games on the same Ephemeral pod (SPECS-2171)

Symptom (two-cluster run): the first game on an Ephemeral pod works, the second game does not pair and the
first player logs `TCPCheck for '<peer ip>:30000' failed`. The upstream repository has the same problem as a disabled
integration test (`XIt("starts 2 games in parallel")`, SPECS-2171). Nothing in the discovery logic was changed by the
Kubernetes client migration.

The reproduction tests below run without a cluster. What they establish and what is still open:

## What was verified (and is not the cause)

* Discovery reuses the per-pod network and its port for every later game of the same pod
  (`ServiceNG.networks[pod]`, `TestRegisterSamePodsInConsecutiveGames`). Two consecutive games with different game ids
  on the same pods both run through to `GameSuccess`, also when the first one failed
  (`TestTwoConsecutiveGamesOnSamePodsBothSucceed`, `TestGameErrorInFirstGameDoesNotAffectSecond`). The discovery
  bookkeeping is therefore not what breaks the second game.
* `ephemeral.Server` called twice in a row does not hand the first game's response or error to the second request
  (`TestSecondActivation*`).
* A proxy can be restarted on the same local port after a game (`TestProxyRestartOnSameLocalPort`).

## Defects found with the tests (fixes reverted)

The fixes listed below were made during the Kubernetes client migration and have been **reverted** again so that the
migration does not change the behaviour of the services (behaviour parity with upstream `ae8710e`). The column "Fix"
describes the reverted change. The corresponding tests are skipped with `known: SPECS-2171`, or were changed to pin the
original behaviour, or were removed (`TestClosedStreamIsUnsubscribedFromBroadcast`); `go test -race` reports the data
races again. The log line `MPC execution did not terminate in time` mentioned below no longer exists.

| Defect | Effect on a later game | Fix | Test |
| --- | --- | --- | --- |
| `Proxy.Stop` waited on the `activeProxyIndicatorCh` of the previous game when `Run` failed before the proxy was started (e.g. `TCPCheck ... failed`), and then called `Wait` on a proxy that never ran. | After a first successful game, a failing second game never returned from `SPDZEngine.Activate`; the player FSM callback hung until the HTTP timeout. This is the failure mode seen in the log. | indicator channel is reset/created synchronously in `Run` (also removes a data race) | `TestProxyStopAfterFailedRunFollowingSuccessfulGame` |
| `SPDZEngine.Activate` returned as soon as the result was read; the MPC process (`Player-Online.x`) and the tuple streamers kept running in the background (`go s.startMPC`). | The next game on the pod starts while the previous one still holds the SPDZ port (5000+id), the ip file and tuple pipes. The race detector reports this overlap in the existing tests. | `Activate` waits (at most `mpcShutdownGrace`, 5s) for `startMPC` to terminate | `go test -race ./pkg/ephemeral` |
| `startMPC` / `SPDZWrapper.Execute` wrote errors to a buffered(1) channel with a blocking send. | A second error (or a request that already returned) pinned the goroutine and thereby the SPDZ process of a finished game. | non-blocking `reportError` | `TestSPDZWrapperDoesNotBlockAfterRequestGaveUp` |
| `Server.respCh` was unbuffered and the handler read the (shared) server fields after starting the player. | The engine of a game whose request timed out blocked forever on `respCh <- result`; a second request replacing the fields while the first still runs mixed up channels. | `respCh` buffered, channels captured per request | `TestSecondActivation*`, `TestSPDZWrapperDoesNotBlock...` |
| `TransportServer.Events` unsubscribed with a new closure, which never matches (the message bus compares handlers by identity). | Every finished game left a broadcast subscription behind that keeps writing to the dead stream (error log per event for the rest of the process lifetime). | unsubscribe with the subscribed function value | `TestClosedStreamIsUnsubscribedFromBroadcast` |
| `FSM.Run` wrote `current = Stopped` without the lock that `Current()` takes (`ServiceNG.verifyGameState`). | Data race on the state of a finished game when a late event arrives. | guarded by the FSM mutex | `go test -race ./pkg/discovery` |
| `IstioNetworker.sync` ignored the error of listing the gateways and synced with an empty list. | An API hiccup released every port that is in use; the next pod gets a port that is already served by another Gateway. | the error is returned, the port state is untouched | `TestSyncFailureIsNotReported` (pins the original behaviour) |
| `NewPortsState` accepted inverted/out-of-bounds ranges (`100:1`, `0:10`, `1:70000`). | `GetFreePort` handed out ports outside of the range. | range validation | `TestNewPortsStateValidation`, `FuzzNewPortsState` |

## Not fixed (documented, tests skipped with `known: SPECS-2171`)

* Ports are released while their Gateway does not exist yet
  (`TestSyncBeforeGatewayExistsDoesNotReleaseFreshPort`). `PortsState.Sync` derives the used ports only from existing
  Gateways. `CreateNetwork` hands out a port before the network controller created the Gateway, so the periodic sync
  (every 15s) can release that port and hand it out again to the next pod. Two pods then share a gateway port; only
  the first Gateway/VirtualService wins, the traffic of the second pod is routed to the wrong pod, which looks exactly
  like "the peer does not answer". Fixing it needs a design decision (track allocated-but-not-yet-visible ports with
  a grace period, or add the Network resources to the used set); the existing port state tests encode the current
  immediate-release semantics, so it was not changed here.
* Finished games stay in `ServiceNG.games`/`players` (`TestFinishedGamesAreRemovedFromBookkeeping`). Reusing a
  finished game id is answered with `GameProtocolError` (`TestReusingAFinishedGameIDIsRejected`), which is fine for
  UUID game ids, but the maps grow for the lifetime of the process. Removing the entries on `GameDone` is not safe on
  its own: a late event (e.g. a slow player's `GameFinishedWithError`) would re-create the game with a fresh FSM, run
  into the state timeout and broadcast a spurious `GameError`. A tombstone (remember finished ids, drop late events)
  is needed first. The `Network` resource of a pod is deleted by the pod informer only (`networker.go`), the
  discovery bookkeeping of that pod by `DeleteCallback`.
* Foreign ports are cached for the process lifetime (`TestForeignPlayerKeepsPortOfItsHome`): the master keeps the
  port that a slave announced first for a pod, even if the slave later announces a different one (e.g. after the
  slave's `Network` was recreated because its pod was replaced under the same name).

## Open: the exact trigger of `TCPCheck ... failed`

`TCPCheck` of player i against player j > i succeeds only if the SPDZ process of player j is listening on its
port behind the gateway. A failure in the second game therefore means that on the peer pod either (a) the SPDZ process
of the previous game still occupied the port / had not terminated (addressed above for the pod itself: `Activate` now
waits for it; but `Commander` only kills `script` when the context is cancelled, not the whole process tree), or
(b) the port/gateway of the pod is shared with or taken over by another pod (the sync race above), or (c) the peer
never reached `Execute`. This cannot be told apart without a cluster; the new log line
`MPC execution did not terminate in time` and the tests above allow to check (a) and (b) in the next two-cluster run.
On the peer pod after the first game check `ss -ltnp` for port 5000+id, and
`kubectl get gateways.networking.istio.io -o jsonpath='{..port.number}'` for duplicated ports.
