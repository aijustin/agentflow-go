package tier

import "time"

// Policy configures automatic promotion, demotion, and eviction between tiers.
type Policy struct {
	HotCapacity   int
	WarmCapacity  int
	ColdCapacity  int
	HotTTL        time.Duration
	WarmTTL       time.Duration
	PromoteAccess int
	DemoteIdle    time.Duration
}

// ShouldPromote reports whether a record should move to a hotter tier after access.
func (p Policy) ShouldPromote(record Record, now time.Time) bool {
	if record.Pinned {
		return false
	}
	if p.PromoteAccess <= 0 {
		return false
	}
	if record.AccessCount < p.PromoteAccess {
		return false
	}
	switch record.Tier {
	case LevelHot:
		return false
	case LevelWarm:
		return true
	case LevelCold:
		return record.AccessCount >= p.PromoteAccess*2
	default:
		return false
	}
}

// ShouldDemote reports whether a record should move to a colder tier.
func (p Policy) ShouldDemote(record Record, now time.Time, levelCount int) bool {
	if record.Pinned {
		return false
	}
	switch record.Tier {
	case LevelHot:
		if p.HotCapacity > 0 && levelCount > p.HotCapacity {
			return true
		}
		return p.expired(record.LastAccessAt, now, p.HotTTL)
	case LevelWarm:
		if p.WarmCapacity > 0 && levelCount > p.WarmCapacity {
			return true
		}
		return p.expired(record.LastAccessAt, now, p.WarmTTL) || p.idle(record.LastAccessAt, now)
	case LevelCold:
		if p.ColdCapacity > 0 && levelCount > p.ColdCapacity {
			return true
		}
		return p.idle(record.LastAccessAt, now)
	default:
		return false
	}
}

// TargetTier returns the tier a record should occupy after promotion or demotion.
func (p Policy) TargetTier(record Record, now time.Time, levelCount map[Level]int) Level {
	if p.ShouldPromote(record, now) {
		switch record.Tier {
		case LevelCold:
			return LevelWarm
		case LevelWarm:
			return LevelHot
		}
	}
	if p.ShouldDemote(record, now, levelCount[record.Tier]) {
		switch record.Tier {
		case LevelHot:
			return LevelWarm
		case LevelWarm:
			return LevelCold
		}
	}
	return record.Tier
}

// NextTierOnDemote returns the colder tier, or empty if eviction is required.
func (p Policy) NextTierOnDemote(level Level) (Level, bool) {
	switch level {
	case LevelHot:
		return LevelWarm, true
	case LevelWarm:
		return LevelCold, true
	default:
		return "", false
	}
}

func (p Policy) expired(lastAccess, now time.Time, ttl time.Duration) bool {
	if ttl <= 0 || lastAccess.IsZero() {
		return false
	}
	return now.Sub(lastAccess) > ttl
}

func (p Policy) idle(lastAccess, now time.Time) bool {
	if p.DemoteIdle <= 0 || lastAccess.IsZero() {
		return false
	}
	return now.Sub(lastAccess) > p.DemoteIdle
}
