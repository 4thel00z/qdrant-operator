package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
)

const ShardActive = "Active"

type ShardOperationKind string

const (
	ShardMove      ShardOperationKind = "move"
	ShardReplicate ShardOperationKind = "replicate"
	ShardDrop      ShardOperationKind = "drop"
)

type Peer struct {
	PeerID uint64
	URI    string
}

// Ordinal is the StatefulSet ordinal when the peer is a pod of this release.
func (p Peer) Ordinal(release string) (int, bool) {
	pattern, err := regexp.Compile("//" + regexp.QuoteMeta(release) + "-([0-9]+)\\.")
	if err != nil {
		return 0, false
	}
	match := pattern.FindStringSubmatch(p.URI)
	if match == nil {
		return 0, false
	}
	ordinal, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return ordinal, true
}

// ClusterTopology is what one node reports on GET /cluster: its own peer id and every peer it knows.
type ClusterTopology struct {
	PeerID uint64
	Peers  []Peer
}

type Replica struct {
	ShardID int
	PeerID  uint64
	State   string
}

// CollectionShards is one collection's replicas; ReplicationFactor has no default, callers set it.
type CollectionShards struct {
	Collection        string
	Replicas          []Replica
	ReplicationFactor int
	Transfers         int
}

func (c CollectionShards) Holders(shardID int) []Replica {
	holders := []Replica{}
	for _, replica := range c.Replicas {
		if replica.ShardID == shardID {
			holders = append(holders, replica)
		}
	}
	return holders
}

func (c CollectionShards) ShardIDs() []int {
	ids := []int{}
	for _, replica := range c.Replicas {
		if !slices.Contains(ids, replica.ShardID) {
			ids = append(ids, replica.ShardID)
		}
	}
	slices.Sort(ids)
	return ids
}

// Inactive lists replicas not Active as "collection/shard@peer is State".
func (c CollectionShards) Inactive() []string {
	inactive := []string{}
	for _, replica := range c.Replicas {
		if replica.State == ShardActive {
			continue
		}
		inactive = append(inactive, fmt.Sprintf("%s/%d@%d is %s", c.Collection, replica.ShardID, replica.PeerID, replica.State))
	}
	return inactive
}

func (c CollectionShards) OnPeers(peerIDs []uint64) []Replica {
	found := []Replica{}
	for _, replica := range c.Replicas {
		if slices.Contains(peerIDs, replica.PeerID) {
			found = append(found, replica)
		}
	}
	return found
}

// ShardOperation is one cluster-API operation on a shard replica: move or replicate PeerID to TargetPeerID, or drop on PeerID.
type ShardOperation struct {
	Kind         ShardOperationKind
	Collection   string
	ShardID      int
	PeerID       uint64
	TargetPeerID *uint64
}

func (o ShardOperation) Body() JSON {
	if o.Kind == ShardDrop {
		return JSON{"drop_replica": JSON{"shard_id": o.ShardID, "peer_id": o.PeerID}}
	}
	key := "replicate_shard"
	if o.Kind == ShardMove {
		key = "move_shard"
	}
	var target any
	if o.TargetPeerID != nil {
		target = *o.TargetPeerID
	}
	return JSON{key: JSON{"shard_id": o.ShardID, "from_peer_id": o.PeerID, "to_peer_id": target}}
}

func (o ShardOperation) Describe() string {
	target := ""
	if o.TargetPeerID != nil {
		target = fmt.Sprintf("->%d", *o.TargetPeerID)
	}
	return fmt.Sprintf("%s %s/%d %d%s", o.Kind, o.Collection, o.ShardID, o.PeerID, target)
}

// ShardLoss is a shard with no healthy replica outside the peers being removed.
type ShardLoss struct {
	Collection string
	ShardID    int
}

func (l ShardLoss) Describe() string {
	return fmt.Sprintf("%s/%d", l.Collection, l.ShardID)
}

type ShardPlan struct {
	Operations []ShardOperation
	Lost       []ShardLoss
	InFlight   int
	Blocked    []string
}

func newShardPlan(operations []ShardOperation, lost []ShardLoss, survey []CollectionShards, blocked []string) ShardPlan {
	inFlight := 0
	for _, shards := range survey {
		inFlight += shards.Transfers
	}
	return ShardPlan{Operations: operations, Lost: lost, InFlight: inFlight, Blocked: blocked}
}

func idSet(ids []uint64) map[uint64]bool {
	set := map[uint64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func PeerLoad(survey []CollectionShards, peerIDs []uint64) map[uint64]int {
	counts := map[uint64]int{}
	for _, id := range peerIDs {
		counts[id] = 0
	}
	for _, shards := range survey {
		for _, replica := range shards.Replicas {
			if _, ok := counts[replica.PeerID]; ok {
				counts[replica.PeerID]++
			}
		}
	}
	return counts
}

// LeastLoaded picks the lowest (count, peer id) among peers not excluded.
func LeastLoaded(load map[uint64]int, excluded []uint64) (uint64, bool) {
	barred := idSet(excluded)
	best := uint64(0)
	found := false
	for id, count := range load {
		if barred[id] {
			continue
		}
		if !found || count < load[best] || (count == load[best] && id < best) {
			best = id
			found = true
		}
	}
	return best, found
}

func replicaPeerIDs(replicas []Replica) []uint64 {
	ids := []uint64{}
	for _, replica := range replicas {
		ids = append(ids, replica.PeerID)
	}
	return ids
}

func anyBusy(ids []uint64, busy map[uint64]bool) bool {
	for _, id := range ids {
		if busy[id] {
			return true
		}
	}
	return false
}

// PlanEvacuation drops redundant replicas on the leaving peers and moves the rest to the least loaded staying peer.
// A collection with a running transfer is blocked, and a peer takes part in at most one transfer per plan.
func PlanEvacuation(survey []CollectionShards, leaving []uint64, staying []uint64) ShardPlan {
	load := PeerLoad(survey, staying)
	busy := map[uint64]bool{}
	operations := []ShardOperation{}
	lost := []ShardLoss{}
	blocked := []string{}
	for _, shards := range survey {
		stranded := shards.OnPeers(leaving)
		if len(stranded) == 0 {
			continue
		}
		if shards.Transfers != 0 {
			blocked = append(blocked, shards.Collection)
			continue
		}
		for _, replica := range stranded {
			holders := shards.Holders(replica.ShardID)
			kept := 0
			for _, holder := range holders {
				if slices.Contains(staying, holder.PeerID) && holder.State == ShardActive {
					kept++
				}
			}
			target, hasTarget := LeastLoaded(load, replicaPeerIDs(holders))
			if kept > 0 && (kept >= shards.ReplicationFactor || !hasTarget) {
				operations = append(operations, ShardOperation{Kind: ShardDrop, Collection: shards.Collection, ShardID: replica.ShardID, PeerID: replica.PeerID})
				continue
			}
			if !hasTarget || replica.State != ShardActive {
				lost = append(lost, ShardLoss{Collection: shards.Collection, ShardID: replica.ShardID})
				continue
			}
			if anyBusy([]uint64{replica.PeerID, target}, busy) {
				continue
			}
			busy[replica.PeerID] = true
			busy[target] = true
			load[target]++
			moveTarget := target
			operations = append(operations, ShardOperation{Kind: ShardMove, Collection: shards.Collection, ShardID: replica.ShardID, PeerID: replica.PeerID, TargetPeerID: &moveTarget})
		}
	}
	return newShardPlan(operations, lost, survey, blocked)
}

// PlanRecovery replicates shards that lost replicas on dead peers from a live Active replica to a live peer lacking one.
func PlanRecovery(survey []CollectionShards, dead []uint64, live []uint64) ShardPlan {
	load := PeerLoad(survey, live)
	busy := map[uint64]bool{}
	operations := []ShardOperation{}
	lost := []ShardLoss{}
	blocked := []string{}
	for _, shards := range survey {
		stranded := shards.OnPeers(dead)
		if len(stranded) == 0 {
			continue
		}
		if shards.Transfers != 0 {
			blocked = append(blocked, shards.Collection)
			continue
		}
		shardIDs := []int{}
		for _, replica := range stranded {
			if !slices.Contains(shardIDs, replica.ShardID) {
				shardIDs = append(shardIDs, replica.ShardID)
			}
		}
		slices.Sort(shardIDs)
		for _, shardID := range shardIDs {
			holders := shards.Holders(shardID)
			sources := []Replica{}
			liveHolders := 0
			for _, holder := range holders {
				if !slices.Contains(live, holder.PeerID) {
					continue
				}
				liveHolders++
				if holder.State == ShardActive {
					sources = append(sources, holder)
				}
			}
			if len(sources) == 0 {
				lost = append(lost, ShardLoss{Collection: shards.Collection, ShardID: shardID})
				continue
			}
			if liveHolders >= shards.ReplicationFactor {
				continue
			}
			target, hasTarget := LeastLoaded(load, replicaPeerIDs(holders))
			source := sources[0].PeerID
			if !hasTarget || anyBusy([]uint64{source, target}, busy) {
				continue
			}
			busy[source] = true
			busy[target] = true
			load[target]++
			replicateTarget := target
			operations = append(operations, ShardOperation{Kind: ShardReplicate, Collection: shards.Collection, ShardID: shardID, PeerID: source, TargetPeerID: &replicateTarget})
		}
	}
	return newShardPlan(operations, lost, survey, blocked)
}

// DepartingPeers are the pods of this release with an ordinal at or above the new replica count, sorted.
func DepartingPeers(topology ClusterTopology, release string, replicas int) []uint64 {
	ids := []uint64{}
	for _, peer := range topology.Peers {
		ordinal, ok := peer.Ordinal(release)
		if ok && ordinal >= replicas && !slices.Contains(ids, peer.PeerID) {
			ids = append(ids, peer.PeerID)
		}
	}
	slices.Sort(ids)
	return ids
}

func SurvivingPeers(topology ClusterTopology, release string, replicas int) []uint64 {
	ids := []uint64{}
	for _, peer := range topology.Peers {
		ordinal, ok := peer.Ordinal(release)
		if ok && ordinal < replicas {
			ids = append(ids, peer.PeerID)
		}
	}
	slices.Sort(ids)
	return ids
}

// DeadPeers are peers no pod answers for, judged only when every pod of the release reported its id.
// A peer is dead when the pod with its ordinal reports another id or its ordinal is beyond the replica count.
func DeadPeers(topology ClusterTopology, podPeerIDs map[int]uint64, release string, replicas int) []uint64 {
	ids := []uint64{}
	if len(podPeerIDs) != max(replicas, 0) {
		return ids
	}
	for ordinal := range replicas {
		if _, ok := podPeerIDs[ordinal]; !ok {
			return ids
		}
	}
	for _, peer := range topology.Peers {
		ordinal, ok := peer.Ordinal(release)
		if !ok || slices.Contains(ids, peer.PeerID) {
			continue
		}
		if ordinal >= replicas || podPeerIDs[ordinal] != peer.PeerID {
			ids = append(ids, peer.PeerID)
		}
	}
	slices.Sort(ids)
	return ids
}

var replacedPeerPattern = regexp.MustCompile("already used by peer ([0-9]+)")

// ReplacedPeer reads the peer id a pod on an empty volume cannot take over from Qdrant's join error.
func ReplacedPeer(log string) (uint64, bool) {
	match := replacedPeerPattern.FindStringSubmatch(log)
	if match == nil {
		return 0, false
	}
	id, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func Settled(shards CollectionShards) bool {
	if shards.Transfers != 0 {
		return false
	}
	for _, replica := range shards.Replicas {
		if replica.State != ShardActive {
			return false
		}
	}
	return true
}

// PlanRebalance yields at most one operation per settled collection: drop beyond the replication factor,
// replicate below it, then move from the busiest to the idlest peer. A collection with a replica outside live is blocked.
func PlanRebalance(survey []CollectionShards, live []uint64) ShardPlan {
	busy := map[uint64]bool{}
	operations := []ShardOperation{}
	blocked := []string{}
	for _, shards := range survey {
		outside := slices.ContainsFunc(shards.Replicas, func(r Replica) bool { return !slices.Contains(live, r.PeerID) })
		if !Settled(shards) || outside {
			blocked = append(blocked, shards.Collection)
			continue
		}
		operation, ok := NextRebalanceStep(shards, live, busy)
		if !ok {
			continue
		}
		busy[operation.PeerID] = true
		if operation.TargetPeerID != nil {
			busy[*operation.TargetPeerID] = true
		}
		operations = append(operations, operation)
	}
	return newShardPlan(operations, []ShardLoss{}, survey, blocked)
}

func NextRebalanceStep(shards CollectionShards, live []uint64, busy map[uint64]bool) (ShardOperation, bool) {
	load := PeerLoad([]CollectionShards{shards}, live)
	idle := map[uint64]int{}
	for id, count := range load {
		if !busy[id] {
			idle[id] = count
		}
	}
	for _, shardID := range shards.ShardIDs() {
		all := shards.Holders(shardID)
		holders := []uint64{}
		for _, holder := range all {
			if _, ok := idle[holder.PeerID]; ok {
				holders = append(holders, holder.PeerID)
			}
		}
		if len(all) <= shards.ReplicationFactor || len(holders) == 0 {
			continue
		}
		busiest := holders[0]
		for _, id := range holders[1:] {
			if load[id] > load[busiest] || (load[id] == load[busiest] && id > busiest) {
				busiest = id
			}
		}
		return ShardOperation{Kind: ShardDrop, Collection: shards.Collection, ShardID: shardID, PeerID: busiest}, true
	}
	for _, shardID := range shards.ShardIDs() {
		all := shards.Holders(shardID)
		if len(all) >= shards.ReplicationFactor {
			continue
		}
		sources := []uint64{}
		for _, holder := range all {
			if _, ok := idle[holder.PeerID]; ok {
				sources = append(sources, holder.PeerID)
			}
		}
		target, hasTarget := LeastLoaded(idle, replicaPeerIDs(all))
		if len(sources) == 0 || !hasTarget {
			continue
		}
		return ShardOperation{Kind: ShardReplicate, Collection: shards.Collection, ShardID: shardID, PeerID: sources[0], TargetPeerID: &target}, true
	}
	return NextBalancingMove(shards, load, idle)
}

func NextBalancingMove(shards CollectionShards, load map[uint64]int, idle map[uint64]int) (ShardOperation, bool) {
	if len(idle) < 2 {
		return ShardOperation{}, false
	}
	busiest, idlest := uint64(0), uint64(0)
	first := true
	for id, count := range idle {
		if first {
			busiest, idlest, first = id, id, false
			continue
		}
		if count > idle[busiest] || (count == idle[busiest] && id > busiest) {
			busiest = id
		}
		if count < idle[idlest] || (count == idle[idlest] && id < idlest) {
			idlest = id
		}
	}
	if load[busiest]-load[idlest] <= 1 {
		return ShardOperation{}, false
	}
	held := map[int]bool{}
	for _, replica := range shards.Replicas {
		if replica.PeerID == idlest {
			held[replica.ShardID] = true
		}
	}
	for _, replica := range shards.Replicas {
		if replica.PeerID == busiest && !held[replica.ShardID] {
			return ShardOperation{Kind: ShardMove, Collection: shards.Collection, ShardID: replica.ShardID, PeerID: busiest, TargetPeerID: &idlest}, true
		}
	}
	return ShardOperation{}, false
}
