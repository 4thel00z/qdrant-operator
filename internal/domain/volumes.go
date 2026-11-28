package domain

import (
	"fmt"
	"slices"
	"strings"
)

type VolumeClaim struct {
	Name         string
	Size         string
	StorageClass string
	AccessModes  []string
	VolumeName   string
}

func DefaultVolumeClaim(name string, size string) VolumeClaim {
	return VolumeClaim{Name: name, Size: size, AccessModes: []string{"ReadWriteOnce"}}
}

// Zone is a topology zone as a node label: the key the CSI driver uses and its value.
type Zone struct {
	Label string
	Name  string
}

// NewZone returns false unless both the zone name and its label are given.
func NewZone(name string, label string) (Zone, bool) {
	if name == "" || label == "" {
		return Zone{}, false
	}
	return Zone{Label: label, Name: name}, true
}

// DataClaimName is the claim the StatefulSet of a cluster creates for the data volume of one pod.
func DataClaimName(clusterName string, ordinal int) string {
	return fmt.Sprintf("%s-%s-%d", StorageClaim, ReleaseName(clusterName), ordinal)
}

type VolumeSnapshotState struct {
	Name  string
	Ready bool
	Error string
}

type VolumeGrowth struct {
	Name string
	Size string
}

// VolumePlan is what a spec change asks of the volumes: PVCs to grow and whether the template changes.
type VolumePlan struct {
	Grow     []VolumeGrowth
	Recreate bool
}

// PlanVolumes compares desired claim sizes with the StatefulSet's volume claim templates.
// Growing is allowed and shrinking is an error. Any difference in the claim set or sizes
// means the immutable template has to be recreated.
func PlanVolumes(desired map[string]string, liveTemplates map[string]string) (VolumePlan, error) {
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	slices.Sort(names)
	shrunk := []string{}
	grow := []VolumeGrowth{}
	for _, name := range names {
		live, ok := liveTemplates[name]
		if !ok {
			continue
		}
		wanted, err := ParseQuantity(desired[name])
		if err != nil {
			return VolumePlan{}, fmt.Errorf("desired claim %s: %w", name, err)
		}
		current, err := ParseQuantity(live)
		if err != nil {
			return VolumePlan{}, fmt.Errorf("live claim %s: %w", name, err)
		}
		if wanted < current {
			shrunk = append(shrunk, name)
		}
		if wanted > current {
			grow = append(grow, VolumeGrowth{Name: name, Size: desired[name]})
		}
	}
	if len(shrunk) > 0 {
		return VolumePlan{}, fmt.Errorf("volume claims cannot shrink: %s", strings.Join(shrunk, ", "))
	}
	return VolumePlan{Grow: grow, Recreate: len(grow) > 0 || !sameClaimSet(desired, liveTemplates)}, nil
}

func sameClaimSet(desired map[string]string, live map[string]string) bool {
	if len(desired) != len(live) {
		return false
	}
	for name := range desired {
		if _, ok := live[name]; !ok {
			return false
		}
	}
	return true
}
