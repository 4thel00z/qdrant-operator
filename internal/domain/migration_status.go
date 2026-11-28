package domain

import (
	"math"
	"time"
)

// CollectionMigration is the progress of one collection; a nil count means it was not verified yet.
type CollectionMigration struct {
	Name            string
	TargetName      string
	Status          MigrationPhase
	PointsTotal     int64
	PointsCopied    int64
	Offset          PointID
	Error           string
	VerifyPass      int
	SourceCount     *int64
	TargetCount     *int64
	Message         string
	PointsUpdated   int64
	PointsDeleted   int64
	SyncPass        int
	SyncOffset      PointID
	SyncPassChanges int64
}

func NewCollectionMigration(name string, targetName string) CollectionMigration {
	return CollectionMigration{Name: name, TargetName: targetName, Status: MigrationPending}
}

type MigrationProgress struct {
	CollectionsTotal     int
	CollectionsCompleted int
	PointsTotal          int64
	PointsCopied         int64
}

func ProgressOf(collections []CollectionMigration) MigrationProgress {
	progress := MigrationProgress{CollectionsTotal: len(collections)}
	for _, collection := range collections {
		if collection.Status == MigrationCompleted || collection.Status == MigrationFailed {
			progress.CollectionsCompleted++
		}
		progress.PointsTotal += collection.PointsTotal
		progress.PointsCopied += collection.PointsCopied
	}
	return progress
}

// Percentage rounds half to even and reads an empty migration as done once every collection finished.
func (p MigrationProgress) Percentage() int {
	if p.PointsTotal == 0 {
		if p.CollectionsCompleted == p.CollectionsTotal {
			return 100
		}
		return 0
	}
	ratio := float64(100*p.PointsCopied) / float64(p.PointsTotal)
	return int(math.Min(100, math.RoundToEven(ratio)))
}

type MigrationStatus struct {
	Phase          MigrationPhase
	StartTime      time.Time
	CompletionTime time.Time
	Source         string
	Collections    []CollectionMigration
	Error          string
	Conditions     []Condition
}

// ResumePoint is where a previous run left a collection, if it recorded anything.
func (s MigrationStatus) ResumePoint(name string) (CollectionMigration, bool) {
	for _, collection := range s.Collections {
		if collection.Name == name {
			return collection, true
		}
	}
	return CollectionMigration{}, false
}

func (s MigrationStatus) Progress() MigrationProgress {
	return ProgressOf(s.Collections)
}
