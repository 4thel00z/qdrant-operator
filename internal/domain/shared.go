package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type ResourceKind struct {
	Kind   string
	Plural string
}

var (
	Clusters    = ResourceKind{Kind: "QdrantCluster", Plural: "qdrantclusters"}
	Backups     = ResourceKind{Kind: "QdrantBackup", Plural: "qdrantbackups"}
	Schedules   = ResourceKind{Kind: "QdrantBackupSchedule", Plural: "qdrantbackupschedules"}
	Restores    = ResourceKind{Kind: "QdrantRestore", Plural: "qdrantrestores"}
	Collections = ResourceKind{Kind: "QdrantCollection", Plural: "qdrantcollections"}
	AccessKeys  = ResourceKind{Kind: "QdrantAccessKey", Plural: "qdrantaccesskeys"}
	Migrations  = ResourceKind{Kind: "QdrantMigration", Plural: "qdrantmigrations"}
)

type ResourceRef struct {
	Kind      ResourceKind
	Name      string
	Namespace string
}

type ClusterRef struct {
	Name      string
	Namespace string
}

func (r ClusterRef) ResourceRef() ResourceRef {
	return ResourceRef{Kind: Clusters, Name: r.Name, Namespace: r.Namespace}
}

type Condition struct {
	Type               string
	Status             ConditionStatus
	LastTransitionTime time.Time
	Reason             string
	Message            string
}

// SetCondition replaces the condition of the same type and keeps its transition time when the status is unchanged.
func SetCondition(conditions []Condition, update Condition) []Condition {
	result := make([]Condition, 0, len(conditions)+1)
	for _, condition := range conditions {
		if condition.Type != update.Type {
			result = append(result, condition)
			continue
		}
		if condition.Status == update.Status {
			update.LastTransitionTime = condition.LastTransitionTime
		}
	}
	return append(result, update)
}

func FindCondition(conditions []Condition, conditionType string) *Condition {
	for index := range conditions {
		if conditions[index].Type == conditionType {
			return &conditions[index]
		}
	}
	return nil
}

// ParseTime reads an RFC 3339 timestamp; empty gives the zero time.
func ParseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func FormatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

// ValuesDigest fingerprints a helm upgrade's chart version and values; equal digests mean nothing to apply.
func ValuesDigest(version string, values JSON) (string, error) {
	payload, err := json.Marshal(JSON{"chart": version, "values": values})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
