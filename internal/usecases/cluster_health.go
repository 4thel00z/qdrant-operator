package usecases

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

func now(clock Clock) time.Time {
	if clock == nil {
		return SystemClock()
	}
	return clock()
}

func ProgressCondition(clock Clock, conditionType string, active bool, reason string, message string) domain.Condition {
	status := domain.ConditionFalse
	if active {
		status = domain.ConditionTrue
	}
	return domain.Condition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: now(clock),
		Reason:             reason,
		Message:            message,
	}
}

func withProgress(clock Clock, current domain.ClusterStatus, conditionType string, active bool, reason string, message string) domain.ClusterStatus {
	current.Conditions = domain.SetCondition(current.Conditions, ProgressCondition(clock, conditionType, active, reason, message))
	return current
}

func SurveyCollections(ctx context.Context, qdrant ports.Qdrant, node domain.QdrantNode) ([]domain.CollectionShards, error) {
	names, err := qdrant.ListCollections(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	survey := make([]domain.CollectionShards, 0, len(names))
	for _, name := range names {
		shards, err := qdrant.CollectionShards(ctx, node, name)
		if err != nil {
			return nil, fmt.Errorf("collection %s shards: %w", name, err)
		}
		survey = append(survey, shards)
	}
	return survey, nil
}

// NodeProblems lists what is off on one node; a node that does not answer is a problem, not an error.
func NodeProblems(ctx context.Context, qdrant ports.Qdrant, spec domain.ClusterSpec, ordinal int, node domain.QdrantNode) ([]string, error) {
	topology, err := qdrant.ClusterTopology(ctx, node)
	var survey []domain.CollectionShards
	if err == nil {
		survey, err = SurveyCollections(ctx, qdrant, node)
	}
	if errors.Is(err, ports.ErrUnreachable) {
		return []string{fmt.Sprintf("node %d does not answer", ordinal)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("node %d: %w", ordinal, err)
	}
	podPeers := []int{}
	for _, peer := range topology.Peers {
		if found, ok := peer.Ordinal(spec.ReleaseName()); ok {
			podPeers = append(podPeers, found)
		}
	}
	slices.Sort(podPeers)
	problems := []string{}
	if len(topology.Peers) != spec.Replicas || !slices.Equal(podPeers, ordinals(spec.Replicas)) {
		problems = append(problems, fmt.Sprintf("node %d lists %d peers for %d pods", ordinal, len(topology.Peers), spec.Replicas))
	}
	for _, shards := range survey {
		if shards.Transfers == 0 {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s has %d shard transfers", shards.Collection, shards.Transfers))
	}
	for _, shards := range survey {
		problems = append(problems, shards.Inactive()...)
	}
	return problems, nil
}

func ordinals(count int) []int {
	result := make([]int, 0, max(count, 0))
	for ordinal := range max(count, 0) {
		result = append(result, ordinal)
	}
	return result
}

// AssessHealth is healthy when every pod is Ready and answering, every shard replica Active, no transfer runs and all peers joined.
func AssessHealth(ctx context.Context, qdrant ports.Qdrant, spec domain.ClusterSpec, nodes []domain.QdrantNode, pods []domain.PodRevision) (domain.ClusterHealth, error) {
	problems := []string{}
	for _, pod := range pods {
		if pod.Ready {
			continue
		}
		problems = append(problems, fmt.Sprintf("pod %d is not Ready", pod.Ordinal))
	}
	if len(problems) > 0 || !spec.Distributed.Enabled {
		return domain.ClusterHealth{Problems: problems}, nil
	}
	for ordinal, node := range nodes {
		found, err := NodeProblems(ctx, qdrant, spec, ordinal, node)
		if err != nil {
			return domain.ClusterHealth{}, err
		}
		for _, problem := range found {
			if slices.Contains(problems, problem) {
				continue
			}
			problems = append(problems, problem)
		}
	}
	return domain.ClusterHealth{Problems: problems}, nil
}

func DescribePlan(plan domain.ShardPlan) string {
	parts := []string{fmt.Sprintf("%d shard operations issued", len(plan.Operations))}
	if plan.InFlight > 0 {
		parts = append(parts, fmt.Sprintf("%d transfers running", plan.InFlight))
	}
	if len(plan.Blocked) > 0 {
		parts = append(parts, "waiting on "+strings.Join(plan.Blocked, ", "))
	}
	return strings.Join(parts, "; ")
}

func planIdle(plan domain.ShardPlan) bool {
	return len(plan.Operations) == 0 && plan.InFlight == 0 && len(plan.Blocked) == 0
}

func joinLimited(items []string, limit int) string {
	return strings.Join(items[:min(len(items), limit)], ", ")
}

func describeLosses(losses []domain.ShardLoss) string {
	described := make([]string, 0, len(losses))
	for _, loss := range losses {
		described = append(described, loss.Describe())
	}
	return strings.Join(described, ", ")
}
