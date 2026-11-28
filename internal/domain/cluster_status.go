package domain

type ClusterPhase string

const (
	PhasePending     ClusterPhase = "Pending"
	PhaseRunning     ClusterPhase = "Running"
	PhaseFailed      ClusterPhase = "Failed"
	PhaseUpgrading   ClusterPhase = "Upgrading"
	PhaseTerminating ClusterPhase = "Terminating"
)

// ClusterStatus is what the operator reports for a QdrantCluster. Empty strings and zero numbers mean absent.
type ClusterStatus struct {
	Phase              ClusterPhase
	Replicas           int
	ReadyReplicas      int
	UpdatedReplicas    int
	CurrentRevision    string
	UpdateRevision     string
	AppliedHash        string
	HelmRelease        string
	Endpoint           string
	Version            string
	Conditions         []Condition
	ObservedGeneration int64
}

func NewClusterStatus(phase ClusterPhase) ClusterStatus {
	return ClusterStatus{Phase: phase}
}

type StatefulSetStatus struct {
	Replicas        int
	ReadyReplicas   int
	UpdatedReplicas int
	CurrentRevision string
	UpdateRevision  string
	Partition       int
}

// Rolling is true while pods still run an older revision than the StatefulSet's target; a held partition counts.
func (s StatefulSetStatus) Rolling() bool {
	return s.UpdateRevision != "" && s.CurrentRevision != s.UpdateRevision
}

type PodState struct {
	Name     string
	Revision string
	Ready    bool
}

type PodRevision struct {
	Ordinal  int
	Revision string
	Ready    bool
}

// PodRevisions returns one entry per ordinal below the replica count; a pod that does not exist is not Ready.
func PodRevisions(release string, replicas int, pods []PodState) []PodRevision {
	found := map[int]PodState{}
	prefix := release + "-"
	for _, pod := range pods {
		ordinal, ok := podOrdinal(prefix, pod.Name)
		if !ok {
			continue
		}
		found[ordinal] = pod
	}
	revisions := make([]PodRevision, 0, max(replicas, 0))
	for ordinal := range max(replicas, 0) {
		pod, ok := found[ordinal]
		if !ok {
			revisions = append(revisions, PodRevision{Ordinal: ordinal})
			continue
		}
		revisions = append(revisions, PodRevision{Ordinal: ordinal, Revision: pod.Revision, Ready: pod.Ready})
	}
	return revisions
}

type ClusterHealth struct {
	Problems []string
}

func (h ClusterHealth) Healthy() bool {
	return len(h.Problems) == 0
}
