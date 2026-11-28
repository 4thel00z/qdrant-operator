package domain

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const rolloutMessageItems = 10

func podOrdinal(prefix string, name string) (int, bool) {
	digits, ok := strings.CutPrefix(name, prefix)
	if !ok || digits == "" {
		return 0, false
	}
	for _, char := range digits {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	ordinal, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return ordinal, true
}

type RolloutSnapshot struct {
	Partition      int
	Replicas       int
	UpdateRevision string
	Pods           []PodRevision
	Health         ClusterHealth
}

func (s RolloutSnapshot) HeldAt() int {
	return min(s.Partition, s.Replicas)
}

// Behind lists the ordinals not at the update revision, highest first.
func (s RolloutSnapshot) Behind() []int {
	if s.UpdateRevision == "" {
		return nil
	}
	ordinals := []int{}
	for _, pod := range s.Pods {
		if pod.Revision != s.UpdateRevision {
			ordinals = append(ordinals, pod.Ordinal)
		}
	}
	slices.SortFunc(ordinals, func(a int, b int) int { return b - a })
	return ordinals
}

func DescribeProblems(problems []string) string {
	shown := strings.Join(problems[:min(len(problems), rolloutMessageItems)], "; ")
	hidden := len(problems) - rolloutMessageItems
	if hidden <= 0 {
		return shown
	}
	return fmt.Sprintf("%s; and %d more", shown, hidden)
}

type RolloutWait struct {
	Reason  string
	Message string
}

// HoldPartition is the partition to keep while no pod is being released.
func HoldPartition(strategy RolloutStrategy, replicas int) int {
	if strategy == RolloutReadiness {
		return 0
	}
	return replicas
}

// Wait reports what a rollout with pods left to update is waiting for; false means it may release one.
func (s RolloutSnapshot) Wait() (RolloutWait, bool) {
	behind := s.Behind()
	if len(behind) == 0 {
		return RolloutWait{}, false
	}
	held := s.HeldAt()
	for _, ordinal := range behind {
		if ordinal >= held {
			return RolloutWait{Reason: "RestartingPod", Message: fmt.Sprintf("pod %d is restarting", ordinal)}, true
		}
	}
	for _, pod := range s.Pods {
		if pod.Ordinal == held && !pod.Ready {
			return RolloutWait{Reason: "RestartingPod", Message: fmt.Sprintf("pod %d is not Ready yet", pod.Ordinal)}, true
		}
	}
	if !s.Health.Healthy() {
		return RolloutWait{Reason: "WaitingForShards", Message: DescribeProblems(s.Health.Problems)}, true
	}
	return RolloutWait{}, false
}

// ReleasedPodsSettled is true when every pod the StatefulSet controller may restart is updated and Ready.
func ReleasedPodsSettled(partition int, updateRevision string, pods []PodRevision) bool {
	for _, pod := range pods {
		if pod.Ordinal < partition {
			continue
		}
		if !pod.Ready || (updateRevision != "" && pod.Revision != updateRevision) {
			return false
		}
	}
	return true
}

// NextPartition is the partition to set now; false leaves it alone. It lowers by at most one pod per call.
func NextPartition(strategy RolloutStrategy, snapshot RolloutSnapshot) (int, bool) {
	hold := HoldPartition(strategy, snapshot.Replicas)
	if strategy == RolloutReadiness {
		return hold, snapshot.Partition != hold
	}
	behind := snapshot.Behind()
	if len(behind) == 0 {
		return hold, snapshot.Partition != hold
	}
	if _, waiting := snapshot.Wait(); waiting {
		return hold, snapshot.Partition > hold
	}
	return behind[0], true
}
