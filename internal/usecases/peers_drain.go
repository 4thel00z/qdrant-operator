package usecases

import (
	"context"
	"fmt"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// DrainPeers moves shard replicas off the peers a scale-down removes, then removes those peers.
type DrainPeers struct {
	Qdrant ports.Qdrant
}

// Execute issues the next operations; an idle plan means the peers were emptied and removed.
func (d DrainPeers) Execute(ctx context.Context, spec domain.ClusterSpec, node domain.QdrantNode) (domain.ShardPlan, error) {
	topology, err := d.Qdrant.ClusterTopology(ctx, node)
	if err != nil {
		return domain.ShardPlan{}, fmt.Errorf("cluster topology: %w", err)
	}
	leaving := domain.DepartingPeers(topology, spec.ReleaseName(), spec.Replicas)
	survey, err := SurveyCollections(ctx, d.Qdrant, node)
	if err != nil {
		return domain.ShardPlan{}, err
	}
	plan := domain.PlanEvacuation(survey, leaving, domain.SurvivingPeers(topology, spec.ReleaseName(), spec.Replicas))
	if len(plan.Lost) > 0 {
		return plan, &ShardsUnsafeError{Message: "no healthy replica to move off the removed peers: " + describeLosses(plan.Lost)}
	}
	for _, operation := range plan.Operations {
		if err := d.Qdrant.ApplyShardOperation(ctx, node, operation); err != nil {
			return plan, fmt.Errorf("apply %s: %w", operation.Describe(), err)
		}
	}
	if !planIdle(plan) {
		return plan, nil
	}
	for _, peerID := range leaving {
		if err := d.Qdrant.RemovePeer(ctx, node, peerID, false); err != nil {
			return plan, fmt.Errorf("remove peer %d: %w", peerID, err)
		}
	}
	return plan, nil
}
