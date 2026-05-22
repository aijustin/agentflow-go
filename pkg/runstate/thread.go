package runstate

// ResolveThreadID returns the thread group identifier for a snapshot.
func ResolveThreadID(snapshot RunSnapshot) string {
	if snapshot.ThreadID != "" {
		return snapshot.ThreadID
	}
	return snapshot.RunID
}

// IndexedThreadID returns the value stored in thread indexes for a snapshot.
func IndexedThreadID(snapshot RunSnapshot) string {
	return ResolveThreadID(snapshot)
}
