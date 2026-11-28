package domain

import "time"

type Snapshot struct {
	Name       string
	Collection string
	SizeBytes  int64
	Checksum   string
}

// SnapshotInfo is a snapshot as a node lists it; a zero CreatedAt means the node did not say.
type SnapshotInfo struct {
	Name      string
	CreatedAt time.Time
	SizeBytes int64
}

// QdrantNode is one reachable Qdrant HTTP endpoint together with what it takes to talk to it.
type QdrantNode struct {
	URL    string
	APIKey string
	CACert string
}

// ClusterConnection holds the resolved addresses of a QdrantCluster: the Service plus every StatefulSet pod.
type ClusterConnection struct {
	Service QdrantNode
	Nodes   []QdrantNode
}
