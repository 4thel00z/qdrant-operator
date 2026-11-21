"""Peers, shard replicas and the pure planners that move them (no I/O)."""

import re
from collections.abc import Iterable
from collections.abc import Mapping
from collections.abc import Sequence
from dataclasses import dataclass
from enum import StrEnum

from qdrant_operator.domain import JsonDict

ACTIVE = "Active"


class ShardOperationKind(StrEnum):
    MOVE = "move"
    REPLICATE = "replicate"
    DROP = "drop"


@dataclass(frozen=True)
class Peer:
    peer_id: int
    uri: str

    def ordinal(self, release: str) -> int | None:
        """StatefulSet ordinal when the peer is a pod of this release, else None."""
        match = re.search(rf"//{re.escape(release)}-([0-9]+)\.", self.uri)
        if not match:
            return None
        return int(match.group(1))


@dataclass(frozen=True)
class ClusterTopology:
    """What one node reports on GET /cluster: its own peer id and every peer it knows."""

    peer_id: int
    peers: tuple[Peer, ...]


@dataclass(frozen=True)
class Replica:
    shard_id: int
    peer_id: int
    state: str


@dataclass(frozen=True)
class CollectionShards:
    collection: str
    replicas: tuple[Replica, ...]
    replication_factor: int = 1
    transfers: int = 0

    def holders(self, shard_id: int) -> tuple[Replica, ...]:
        return tuple(r for r in self.replicas if r.shard_id == shard_id)

    def shard_ids(self) -> tuple[int, ...]:
        return tuple(sorted({r.shard_id for r in self.replicas}))

    def on_peers(self, peer_ids: Iterable[int]) -> tuple[Replica, ...]:
        wanted = frozenset(peer_ids)
        return tuple(r for r in self.replicas if r.peer_id in wanted)


@dataclass(frozen=True)
class ShardOperation:
    """One cluster-API operation on a shard replica: move/replicate source->target, or drop."""

    kind: ShardOperationKind
    collection: str
    shard_id: int
    peer_id: int
    target_peer_id: int | None = None

    def to_body(self) -> JsonDict:
        if self.kind is ShardOperationKind.DROP:
            return {"drop_replica": {"shard_id": self.shard_id, "peer_id": self.peer_id}}
        key = "move_shard" if self.kind is ShardOperationKind.MOVE else "replicate_shard"
        return {
            key: {
                "shard_id": self.shard_id,
                "from_peer_id": self.peer_id,
                "to_peer_id": self.target_peer_id,
            }
        }

    def describe(self) -> str:
        target = f"->{self.target_peer_id}" if self.target_peer_id is not None else ""
        return f"{self.kind.value} {self.collection}/{self.shard_id} {self.peer_id}{target}"


@dataclass(frozen=True)
class ShardLoss:
    """A shard with no healthy replica outside the peers being removed."""

    collection: str
    shard_id: int

    def describe(self) -> str:
        return f"{self.collection}/{self.shard_id}"


@dataclass(frozen=True)
class ShardPlan:
    operations: tuple[ShardOperation, ...] = ()
    lost: tuple[ShardLoss, ...] = ()
    in_flight: int = 0
    blocked: tuple[str, ...] = ()


def peer_load(survey: Sequence[CollectionShards], peer_ids: Iterable[int]) -> dict[int, int]:
    counts = dict.fromkeys(peer_ids, 0)
    for shards in survey:
        for replica in shards.replicas:
            if replica.peer_id in counts:
                counts[replica.peer_id] += 1
    return counts


def least_loaded(load: Mapping[int, int], excluded: Iterable[int]) -> int | None:
    barred = frozenset(excluded)
    candidates = [(count, peer_id) for peer_id, count in load.items() if peer_id not in barred]
    if not candidates:
        return None
    return min(candidates)[1]


def plan_evacuation(
    survey: Sequence[CollectionShards], leaving: frozenset[int], staying: Sequence[int]
) -> ShardPlan:
    """Operations that empty the leaving peers: drop replicas that are redundant, move the rest.

    A replica is redundant once the staying peers hold Active copies up to the replication
    factor, or when every staying peer already holds the shard. A collection with a transfer
    already running gets no new operation until it settles, and a peer takes part in at most
    one transfer per plan.
    """
    load = peer_load(survey, staying)
    busy: set[int] = set()
    operations: list[ShardOperation] = []
    lost: list[ShardLoss] = []
    blocked: list[str] = []
    for shards in survey:
        stranded = shards.on_peers(leaving)
        if not stranded:
            continue
        if shards.transfers:
            blocked.append(shards.collection)
            continue
        for replica in stranded:
            holders = shards.holders(replica.shard_id)
            kept = [h for h in holders if h.peer_id in staying and h.state == ACTIVE]
            target = least_loaded(load, [h.peer_id for h in holders])
            if kept and (len(kept) >= shards.replication_factor or target is None):
                operations.append(
                    ShardOperation(
                        ShardOperationKind.DROP,
                        shards.collection,
                        replica.shard_id,
                        replica.peer_id,
                    )
                )
                continue
            if target is None or replica.state != ACTIVE:
                lost.append(ShardLoss(shards.collection, replica.shard_id))
                continue
            if {replica.peer_id, target} & busy:
                continue
            busy.update((replica.peer_id, target))
            load[target] += 1
            operations.append(
                ShardOperation(
                    ShardOperationKind.MOVE,
                    shards.collection,
                    replica.shard_id,
                    replica.peer_id,
                    target,
                )
            )
    in_flight = sum(shards.transfers for shards in survey)
    return ShardPlan(tuple(operations), tuple(lost), in_flight, tuple(blocked))


def plan_recovery(
    survey: Sequence[CollectionShards], dead: frozenset[int], live: Sequence[int]
) -> ShardPlan:
    """Operations that restore the replication factor lost with dead peers.

    A shard with a replica on a dead peer and fewer live replicas than the replication factor is
    replicated from a live Active replica onto a live peer that lacks it; the dead replica itself
    goes with the peer's removal. A peer takes part in at most one transfer per plan.
    """
    load = peer_load(survey, live)
    busy: set[int] = set()
    operations: list[ShardOperation] = []
    lost: list[ShardLoss] = []
    blocked: list[str] = []
    for shards in survey:
        stranded = shards.on_peers(dead)
        if not stranded:
            continue
        if shards.transfers:
            blocked.append(shards.collection)
            continue
        for shard_id in sorted({r.shard_id for r in stranded}):
            holders = shards.holders(shard_id)
            sources = [h for h in holders if h.peer_id in live and h.state == ACTIVE]
            if not sources:
                lost.append(ShardLoss(shards.collection, shard_id))
                continue
            if len([h for h in holders if h.peer_id in live]) >= shards.replication_factor:
                continue
            target = least_loaded(load, [h.peer_id for h in holders])
            if target is None or {sources[0].peer_id, target} & busy:
                continue
            busy.update((sources[0].peer_id, target))
            load[target] += 1
            operations.append(
                ShardOperation(
                    ShardOperationKind.REPLICATE,
                    shards.collection,
                    shard_id,
                    sources[0].peer_id,
                    target,
                )
            )
    in_flight = sum(shards.transfers for shards in survey)
    return ShardPlan(tuple(operations), tuple(lost), in_flight, tuple(blocked))


def departing_peers(topology: ClusterTopology, release: str, replicas: int) -> frozenset[int]:
    """Peers that are pods of this release with an ordinal at or above the new replica count."""
    return frozenset(
        p.peer_id
        for p in topology.peers
        if (ordinal := p.ordinal(release)) is not None and ordinal >= replicas
    )


def surviving_peers(topology: ClusterTopology, release: str, replicas: int) -> tuple[int, ...]:
    return tuple(
        sorted(
            p.peer_id
            for p in topology.peers
            if (ordinal := p.ordinal(release)) is not None and ordinal < replicas
        )
    )


def dead_peers(
    topology: ClusterTopology,
    pod_peer_ids: Mapping[int, int],
    release: str,
    replicas: int,
) -> frozenset[int]:
    """Peers no pod answers for, judged only when every pod of the release reported its id.

    A peer is dead when a live pod with its ordinal reports a different peer id (the pod came
    back on an empty volume), or when its ordinal is beyond the replica count. Peers that are
    not pods of this release, and peers whose pod merely is not answering, are never dead.
    """
    if set(pod_peer_ids) != set(range(replicas)):
        return frozenset()
    return frozenset(
        p.peer_id
        for p in topology.peers
        if (ordinal := p.ordinal(release)) is not None
        and (ordinal >= replicas or pod_peer_ids[ordinal] != p.peer_id)
    )


REPLACED_PEER_PATTERN = re.compile(r"already used by peer ([0-9]+)")


def replaced_peer(log: str) -> int | None:
    """The peer id a pod on an empty volume cannot take over, from Qdrant's join error."""
    match = REPLACED_PEER_PATTERN.search(log)
    if not match:
        return None
    return int(match.group(1))


def settled(shards: CollectionShards) -> bool:
    return not shards.transfers and all(r.state == ACTIVE for r in shards.replicas)


def plan_rebalance(survey: Sequence[CollectionShards], live: Sequence[int]) -> ShardPlan:
    """At most one operation per settled collection toward its replication factor and balance.

    In order: drop a replica beyond the replication factor from the busiest holder, replicate a
    shard short of it onto the idlest peer lacking it, then move a replica from the busiest to
    the idlest peer while their counts differ by more than one. Load counts this collection's
    replicas only, and a peer takes part in at most one operation per plan. A collection with a
    replica on a peer outside `live` is left to the dead-peer repair.
    """
    busy: set[int] = set()
    operations: list[ShardOperation] = []
    blocked: list[str] = []
    for shards in survey:
        if not settled(shards) or any(r.peer_id not in live for r in shards.replicas):
            blocked.append(shards.collection)
            continue
        operation = next_rebalance_step(shards, live, busy)
        if not operation:
            continue
        busy.update(p for p in (operation.peer_id, operation.target_peer_id) if p is not None)
        operations.append(operation)
    in_flight = sum(shards.transfers for shards in survey)
    return ShardPlan(tuple(operations), (), in_flight, tuple(blocked))


def next_rebalance_step(
    shards: CollectionShards, live: Sequence[int], busy: set[int]
) -> ShardOperation | None:
    load = peer_load([shards], live)
    idle = {peer_id: count for peer_id, count in load.items() if peer_id not in busy}
    for shard_id in shards.shard_ids():
        holders = [h.peer_id for h in shards.holders(shard_id) if h.peer_id in idle]
        if len(shards.holders(shard_id)) <= shards.replication_factor or not holders:
            continue
        busiest = max(holders, key=lambda peer_id: (load[peer_id], peer_id))
        return ShardOperation(ShardOperationKind.DROP, shards.collection, shard_id, busiest)
    for shard_id in shards.shard_ids():
        holders = shards.holders(shard_id)
        if len(holders) >= shards.replication_factor:
            continue
        sources = [h.peer_id for h in holders if h.peer_id in idle]
        target = least_loaded(idle, [h.peer_id for h in holders])
        if not sources or target is None:
            continue
        return ShardOperation(
            ShardOperationKind.REPLICATE, shards.collection, shard_id, sources[0], target
        )
    return next_balancing_move(shards, load, idle)


def next_balancing_move(
    shards: CollectionShards, load: Mapping[int, int], idle: Mapping[int, int]
) -> ShardOperation | None:
    if len(idle) < 2:
        return None
    busiest = max(idle, key=lambda peer_id: (idle[peer_id], peer_id))
    idlest = min(idle, key=lambda peer_id: (idle[peer_id], peer_id))
    if load[busiest] - load[idlest] <= 1:
        return None
    held = {r.shard_id for r in shards.replicas if r.peer_id == idlest}
    movable = [
        r.shard_id for r in shards.replicas if r.peer_id == busiest and r.shard_id not in held
    ]
    if not movable:
        return None
    return ShardOperation(ShardOperationKind.MOVE, shards.collection, movable[0], busiest, idlest)
