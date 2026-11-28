package domain_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

type peersReplica struct {
	ShardID int    `json:"shard_id"`
	PeerID  uint64 `json:"peer_id"`
	State   string `json:"state"`
}

type peersShards struct {
	Collection        string         `json:"collection"`
	Replicas          []peersReplica `json:"replicas"`
	ReplicationFactor int            `json:"replication_factor"`
	Transfers         int            `json:"transfers"`
}

type peersOperation struct {
	Kind         string          `json:"kind"`
	Collection   string          `json:"collection"`
	ShardID      int             `json:"shard_id"`
	PeerID       uint64          `json:"peer_id"`
	TargetPeerID *uint64         `json:"target_peer_id"`
	Body         json.RawMessage `json:"body"`
	Describe     string          `json:"describe"`
}

type peersLoss struct {
	Collection string `json:"collection"`
	ShardID    int    `json:"shard_id"`
}

type peersPlan struct {
	Operations    []peersOperation `json:"operations"`
	Lost          []peersLoss      `json:"lost"`
	LostDescribed []string         `json:"lost_described"`
	InFlight      int              `json:"in_flight"`
	Blocked       []string         `json:"blocked"`
}

type peersGolden struct {
	Ordinals []struct {
		Release string `json:"release"`
		URI     string `json:"uri"`
		Ordinal *int   `json:"ordinal"`
	} `json:"ordinals"`
	Topologies []struct {
		Release  string `json:"release"`
		Replicas int    `json:"replicas"`
		Peers    []struct {
			PeerID uint64 `json:"peer_id"`
			URI    string `json:"uri"`
		} `json:"peers"`
		Pods      map[string]uint64 `json:"pods"`
		Departing []uint64          `json:"departing"`
		Surviving []uint64          `json:"surviving"`
		Dead      []uint64          `json:"dead"`
	} `json:"topologies"`
	Replaced []struct {
		Log string  `json:"log"`
		Out *uint64 `json:"out"`
	} `json:"replaced"`
	Evacuation []struct {
		Name    string        `json:"name"`
		Survey  []peersShards `json:"survey"`
		Leaving []uint64      `json:"leaving"`
		Staying []uint64      `json:"staying"`
		Out     peersPlan     `json:"out"`
	} `json:"evacuation"`
	Recovery []struct {
		Name   string        `json:"name"`
		Survey []peersShards `json:"survey"`
		Dead   []uint64      `json:"dead"`
		Live   []uint64      `json:"live"`
		Out    peersPlan     `json:"out"`
	} `json:"recovery"`
	Rebalance []struct {
		Name    string        `json:"name"`
		Survey  []peersShards `json:"survey"`
		Live    []uint64      `json:"live"`
		Out     peersPlan     `json:"out"`
		Settled []bool        `json:"settled"`
	} `json:"rebalance"`
	Loads []struct {
		Survey      []peersShards  `json:"survey"`
		PeerIDs     []uint64       `json:"peer_ids"`
		Load        map[string]int `json:"load"`
		Excluded    []uint64       `json:"excluded"`
		LeastLoaded *uint64        `json:"least_loaded"`
		Inactive    [][]string     `json:"inactive"`
		ShardIDs    [][]int        `json:"shard_ids"`
	} `json:"loads"`
	Operations []peersOperation `json:"operations"`
}

func loadPeersGolden(t *testing.T) peersGolden {
	t.Helper()
	raw, err := os.ReadFile("testdata/peers_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	golden := peersGolden{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&golden); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	return golden
}

func domainSurvey(survey []peersShards) []domain.CollectionShards {
	result := []domain.CollectionShards{}
	for _, shards := range survey {
		replicas := []domain.Replica{}
		for _, replica := range shards.Replicas {
			replicas = append(replicas, domain.Replica{ShardID: replica.ShardID, PeerID: replica.PeerID, State: replica.State})
		}
		result = append(result, domain.CollectionShards{Collection: shards.Collection, Replicas: replicas, ReplicationFactor: shards.ReplicationFactor, Transfers: shards.Transfers})
	}
	return result
}

func canonicalJSON(t *testing.T, raw []byte) string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func actualPlan(t *testing.T, plan domain.ShardPlan) peersPlan {
	t.Helper()
	out := peersPlan{Operations: []peersOperation{}, Lost: []peersLoss{}, LostDescribed: []string{}, InFlight: plan.InFlight, Blocked: plan.Blocked}
	for _, operation := range plan.Operations {
		body, err := json.Marshal(operation.Body())
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		out.Operations = append(out.Operations, peersOperation{
			Kind: string(operation.Kind), Collection: operation.Collection, ShardID: operation.ShardID, PeerID: operation.PeerID,
			TargetPeerID: operation.TargetPeerID, Body: body, Describe: operation.Describe(),
		})
	}
	for _, loss := range plan.Lost {
		out.Lost = append(out.Lost, peersLoss{Collection: loss.Collection, ShardID: loss.ShardID})
		out.LostDescribed = append(out.LostDescribed, loss.Describe())
	}
	return out
}

func comparePlans(t *testing.T, name string, want peersPlan, got peersPlan) {
	t.Helper()
	if len(want.Operations) != len(got.Operations) {
		t.Fatalf("%s: operations: want %+v got %+v", name, want.Operations, got.Operations)
	}
	for index := range want.Operations {
		wantOp, gotOp := want.Operations[index], got.Operations[index]
		if canonicalJSON(t, wantOp.Body) != canonicalJSON(t, gotOp.Body) {
			t.Fatalf("%s: body %d: want %s got %s", name, index, wantOp.Body, gotOp.Body)
		}
		wantOp.Body, gotOp.Body = nil, nil
		if !reflect.DeepEqual(wantOp, gotOp) {
			t.Fatalf("%s: operation %d: want %+v got %+v", name, index, wantOp, gotOp)
		}
	}
	want.Operations, got.Operations = nil, nil
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s: plan: want %+v got %+v", name, want, got)
	}
}

func TestPeerOrdinalMatchesPython(t *testing.T) {
	golden := loadPeersGolden(t)
	if len(golden.Ordinals) == 0 {
		t.Fatal("no ordinal cases")
	}
	for _, c := range golden.Ordinals {
		ordinal, ok := domain.Peer{PeerID: 1, URI: c.URI}.Ordinal(c.Release)
		if (c.Ordinal != nil) != ok || (ok && *c.Ordinal != ordinal) {
			t.Fatalf("%q in %q: want %v got %d,%v", c.Release, c.URI, c.Ordinal, ordinal, ok)
		}
	}
}

func TestPeerSetsMatchPython(t *testing.T) {
	golden := loadPeersGolden(t)
	for _, c := range golden.Topologies {
		topology := domain.ClusterTopology{}
		for _, peer := range c.Peers {
			topology.Peers = append(topology.Peers, domain.Peer{PeerID: peer.PeerID, URI: peer.URI})
		}
		pods := map[int]uint64{}
		for key, peerID := range c.Pods {
			ordinal, err := strconv.Atoi(key)
			if err != nil {
				t.Fatalf("pod key %q: %v", key, err)
			}
			pods[ordinal] = peerID
		}
		if got := domain.DepartingPeers(topology, c.Release, c.Replicas); !slices.Equal(got, c.Departing) {
			t.Fatalf("departing %+v: want %v got %v", c, c.Departing, got)
		}
		if got := domain.SurvivingPeers(topology, c.Release, c.Replicas); !slices.Equal(got, c.Surviving) {
			t.Fatalf("surviving %+v: want %v got %v", c, c.Surviving, got)
		}
		if got := domain.DeadPeers(topology, pods, c.Release, c.Replicas); !slices.Equal(got, c.Dead) {
			t.Fatalf("dead %+v: want %v got %v", c, c.Dead, got)
		}
	}
}

func TestReplacedPeerMatchesPython(t *testing.T) {
	golden := loadPeersGolden(t)
	for _, c := range golden.Replaced {
		id, ok := domain.ReplacedPeer(c.Log)
		if (c.Out != nil) != ok || (ok && *c.Out != id) {
			t.Fatalf("%q: want %v got %d,%v", c.Log, c.Out, id, ok)
		}
	}
}

func TestPlanEvacuationMatchesPython(t *testing.T) {
	golden := loadPeersGolden(t)
	if len(golden.Evacuation) < 100 {
		t.Fatalf("only %d cases", len(golden.Evacuation))
	}
	for _, c := range golden.Evacuation {
		comparePlans(t, c.Name, c.Out, actualPlan(t, domain.PlanEvacuation(domainSurvey(c.Survey), c.Leaving, c.Staying)))
	}
}

func TestPlanRecoveryMatchesPython(t *testing.T) {
	golden := loadPeersGolden(t)
	for _, c := range golden.Recovery {
		comparePlans(t, c.Name, c.Out, actualPlan(t, domain.PlanRecovery(domainSurvey(c.Survey), c.Dead, c.Live)))
	}
}

func TestPlanRebalanceMatchesPython(t *testing.T) {
	golden := loadPeersGolden(t)
	for _, c := range golden.Rebalance {
		survey := domainSurvey(c.Survey)
		comparePlans(t, c.Name, c.Out, actualPlan(t, domain.PlanRebalance(survey, c.Live)))
		for index, shards := range survey {
			if domain.Settled(shards) != c.Settled[index] {
				t.Fatalf("%s: settled %d: want %v", c.Name, index, c.Settled[index])
			}
		}
	}
}

func TestLoadAndCollectionHelpersMatchPython(t *testing.T) {
	golden := loadPeersGolden(t)
	for index, c := range golden.Loads {
		survey := domainSurvey(c.Survey)
		load := domain.PeerLoad(survey, c.PeerIDs)
		want := map[uint64]int{}
		for key, count := range c.Load {
			id, err := strconv.ParseUint(key, 10, 64)
			if err != nil {
				t.Fatalf("load key %q: %v", key, err)
			}
			want[id] = count
		}
		if !reflect.DeepEqual(load, want) {
			t.Fatalf("case %d load: want %v got %v", index, want, load)
		}
		id, ok := domain.LeastLoaded(load, c.Excluded)
		if (c.LeastLoaded != nil) != ok || (ok && *c.LeastLoaded != id) {
			t.Fatalf("case %d least loaded: want %v got %d,%v", index, c.LeastLoaded, id, ok)
		}
		for position, shards := range survey {
			if got := shards.Inactive(); !slices.Equal(got, c.Inactive[position]) {
				t.Fatalf("case %d inactive: want %v got %v", index, c.Inactive[position], got)
			}
			if got := shards.ShardIDs(); !slices.Equal(got, c.ShardIDs[position]) {
				t.Fatalf("case %d shard ids: want %v got %v", index, c.ShardIDs[position], got)
			}
		}
	}
}

func TestShardOperationBodyAndDescribeMatchPython(t *testing.T) {
	golden := loadPeersGolden(t)
	for _, c := range golden.Operations {
		operation := domain.ShardOperation{Kind: domain.ShardOperationKind(c.Kind), Collection: c.Collection, ShardID: c.ShardID, PeerID: c.PeerID, TargetPeerID: c.TargetPeerID}
		body, err := json.Marshal(operation.Body())
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if canonicalJSON(t, body) != canonicalJSON(t, c.Body) {
			t.Fatalf("%+v: body want %s got %s", c, c.Body, body)
		}
		if operation.Describe() != c.Describe {
			t.Fatalf("describe: want %q got %q", c.Describe, operation.Describe())
		}
	}
}
