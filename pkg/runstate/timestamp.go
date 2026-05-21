package runstate

import "time"

// StampSnapshot preserves CreatedAt from a previous snapshot and refreshes UpdatedAt.
func StampSnapshot(snapshot *RunSnapshot, previous *RunSnapshot, now time.Time) {
	if snapshot == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if previous != nil && !previous.CreatedAt.IsZero() {
		snapshot.CreatedAt = previous.CreatedAt
	} else if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = now
	}
	snapshot.UpdatedAt = now
}
