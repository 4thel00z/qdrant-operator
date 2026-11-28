package usecases

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func migrationPause(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func migrationPasses(count int, kind string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s pass", count, kind)
	}
	return fmt.Sprintf("%d %s passes", count, kind)
}

func migrationBatchSize(run migrationRun) int {
	return max(run.spec.BatchSize, 1)
}

func migrationCheckpointEvery(run migrationRun) int {
	return max(run.spec.CheckpointBatches, 1)
}

func (m ExecuteMigration) exactCounts(ctx context.Context, run migrationRun, collection domain.CollectionMigration) (int64, int64, error) {
	sourceCount, err := m.Qdrant.CountPoints(ctx, run.source, collection.Name, true)
	if err != nil {
		return 0, 0, fmt.Errorf("count points of %s: %w", collection.Name, err)
	}
	targetCount, err := m.Qdrant.CountPoints(ctx, run.target, collection.TargetName, true)
	if err != nil {
		return 0, 0, fmt.Errorf("count points of %s: %w", collection.TargetName, err)
	}
	return sourceCount, targetCount, nil
}

// copyAndVerify scrolls, compares exact counts, and rescrolls from the start while the target is short.
func (m ExecuteMigration) copyAndVerify(ctx context.Context, run migrationRun, collection domain.CollectionMigration, shardKeys migrationShardKeys, report migrationReport) (domain.CollectionMigration, error) {
	current := collection
	for {
		copied, err := m.copyPoints(ctx, run, current, shardKeys, report)
		if err != nil {
			return domain.CollectionMigration{}, err
		}
		sourceCount, targetCount, err := m.exactCounts(ctx, run, current)
		if err != nil {
			return domain.CollectionMigration{}, err
		}
		counted := current
		counted.PointsTotal = sourceCount
		counted.PointsCopied = copied
		counted.SourceCount = &sourceCount
		counted.TargetCount = &targetCount
		if targetCount >= sourceCount {
			counted.Status = domain.MigrationCompleted
			counted.PointsCopied = sourceCount
			counted.Offset = domain.PointID{}
			counted.Error = ""
			counted.Message = ""
			if targetCount > sourceCount {
				counted.Message = fmt.Sprintf("target has %d points, more than the %d on the source", targetCount, sourceCount)
			}
			return counted, nil
		}
		if current.VerifyPass >= run.spec.VerifyPasses {
			counted.Status = domain.MigrationFailed
			counted.Offset = domain.PointID{}
			counted.Error = fmt.Sprintf("target has %d of %d source points after %s", targetCount, sourceCount, migrationPasses(current.VerifyPass, "verify"))
			return counted, nil
		}
		current = counted
		current.Status = domain.MigrationRunning
		current.VerifyPass++
		current.PointsCopied = 0
		current.Offset = domain.PointID{}
		if err := report(current); err != nil {
			return domain.CollectionMigration{}, err
		}
	}
}

// copyAndSync copies once, syncs until a pass changes nothing or the cap is reached, then compares exact counts.
func (m ExecuteMigration) copyAndSync(ctx context.Context, run migrationRun, collection domain.CollectionMigration, shardKeys migrationShardKeys, report migrationReport) (domain.CollectionMigration, error) {
	current := collection
	if current.SyncPass == 0 {
		copied, err := m.copyPoints(ctx, run, current, shardKeys, report)
		if err != nil {
			return domain.CollectionMigration{}, err
		}
		current.PointsCopied = copied
		current.Offset = domain.PointID{}
		current.SyncPass = 1
		current.SyncOffset = domain.PointID{}
		current.SyncPassChanges = 0
		if err := report(current); err != nil {
			return domain.CollectionMigration{}, err
		}
	}
	for {
		synced, err := m.syncPass(ctx, run, current, shardKeys, report)
		if err != nil {
			return domain.CollectionMigration{}, err
		}
		current = synced
		if current.SyncPassChanges == 0 || current.SyncPass >= run.spec.SyncMaxPasses {
			break
		}
		current.SyncPass++
		current.SyncOffset = domain.PointID{}
		current.SyncPassChanges = 0
		if err := report(current); err != nil {
			return domain.CollectionMigration{}, err
		}
	}
	sourceCount, targetCount, err := m.exactCounts(ctx, run, current)
	if err != nil {
		return domain.CollectionMigration{}, err
	}
	counted := current
	counted.PointsTotal = sourceCount
	counted.PointsCopied = sourceCount
	counted.SourceCount = &sourceCount
	counted.TargetCount = &targetCount
	counted.Offset = domain.PointID{}
	counted.SyncOffset = domain.PointID{}
	if targetCount < sourceCount {
		counted.Status = domain.MigrationFailed
		counted.Error = fmt.Sprintf("target has %d of %d source points after %s", targetCount, sourceCount, migrationPasses(current.SyncPass, "sync"))
		return counted, nil
	}
	notes := []string{}
	if targetCount > sourceCount {
		notes = append(notes, fmt.Sprintf("target has %d points, more than the %d on the source", targetCount, sourceCount))
	}
	if current.SyncPassChanges > 0 && run.spec.SyncMaxPasses > 1 {
		notes = append(notes, fmt.Sprintf("sync pass %d still changed %d points", current.SyncPass, current.SyncPassChanges))
	}
	counted.Status = domain.MigrationCompleted
	counted.Error = ""
	counted.Message = strings.Join(notes, "; ")
	return counted, nil
}

func (m ExecuteMigration) syncPass(ctx context.Context, run migrationRun, collection domain.CollectionMigration, shardKeys migrationShardKeys, report migrationReport) (domain.CollectionMigration, error) {
	current := collection
	batchSize := migrationBatchSize(run)
	offset := collection.SyncOffset
	windows := 0
	for {
		points, following, err := m.Qdrant.ScrollPoints(ctx, run.source, current.Name, offset, batchSize)
		if err != nil {
			return domain.CollectionMigration{}, fmt.Errorf("scroll %s: %w", current.Name, err)
		}
		existing, err := m.targetRange(ctx, run, current.TargetName, offset, following, batchSize)
		if err != nil {
			return domain.CollectionMigration{}, err
		}
		changed, stale, err := domain.PlanSyncWindow(points, existing)
		if err != nil {
			return domain.CollectionMigration{}, err
		}
		if err := m.writePage(ctx, run, current, shardKeys, changed); err != nil {
			return domain.CollectionMigration{}, err
		}
		for start := 0; start < len(stale); start += batchSize {
			ids := stale[start:min(start+batchSize, len(stale))]
			if err := m.Qdrant.DeletePoints(ctx, run.target, current.TargetName, ids); err != nil {
				return domain.CollectionMigration{}, fmt.Errorf("delete points from %s: %w", current.TargetName, err)
			}
		}
		current.PointsUpdated += int64(len(changed))
		current.PointsDeleted += int64(len(stale))
		current.SyncPassChanges += int64(len(changed) + len(stale))
		current.SyncOffset = following
		windows++
		if following.IsZero() || len(points) == 0 {
			current.SyncOffset = domain.PointID{}
			return current, nil
		}
		offset = following
		if windows%migrationCheckpointEvery(run) == 0 {
			if err := report(current); err != nil {
				return domain.CollectionMigration{}, err
			}
		}
		if err := migrationPause(ctx, run.spec.BatchDelay); err != nil {
			return domain.CollectionMigration{}, err
		}
	}
}

// targetRange reads the target points with start <= id < stop in ID order; a zero stop reads to the end.
func (m ExecuteMigration) targetRange(ctx context.Context, run migrationRun, name string, start domain.PointID, stop domain.PointID, limit int) ([]domain.Point, error) {
	found := []domain.Point{}
	cursor := start
	for {
		points, next, err := m.Qdrant.ScrollPoints(ctx, run.target, name, cursor, limit)
		if err != nil {
			return nil, fmt.Errorf("scroll %s: %w", name, err)
		}
		for _, point := range points {
			if !stop.IsZero() && domain.ComparePointIDs(point.ID, stop) >= 0 {
				return found, nil
			}
			found = append(found, point)
		}
		if next.IsZero() || len(points) == 0 {
			return found, nil
		}
		cursor = next
	}
}

func (m ExecuteMigration) copyPoints(ctx context.Context, run migrationRun, collection domain.CollectionMigration, shardKeys migrationShardKeys, report migrationReport) (int64, error) {
	offset := collection.Offset
	copied := int64(0)
	if !collection.Offset.IsZero() {
		copied = collection.PointsCopied
	}
	batches := 0
	for {
		points, next, err := m.Qdrant.ScrollPoints(ctx, run.source, collection.Name, offset, migrationBatchSize(run))
		if err != nil {
			return 0, fmt.Errorf("scroll %s: %w", collection.Name, err)
		}
		offset = next
		if err := m.writePage(ctx, run, collection, shardKeys, points); err != nil {
			return 0, err
		}
		copied += int64(len(points))
		batches++
		if offset.IsZero() || len(points) == 0 {
			return copied, nil
		}
		if batches%migrationCheckpointEvery(run) == 0 {
			progress := collection
			progress.PointsCopied = copied
			progress.Offset = offset
			if err := report(progress); err != nil {
				return 0, err
			}
		}
		if err := migrationPause(ctx, run.spec.BatchDelay); err != nil {
			return 0, err
		}
	}
}
