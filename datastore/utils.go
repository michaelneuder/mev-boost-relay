package datastore

func MakeBlockBuilderStatus(s BlockBuilderStatus) BlockBuilderStatusStr {
	if s.Blacklisted {
		return RedisBlockBuilderStatusBlacklisted
	} else if s.Optimistic {
		return RedisBlockBuilderStatusOptimistic
	} else if s.HighPrio {
		return RedisBlockBuilderStatusHighPrio
	} else {
		return RedisBlockBuilderStatusLowPrio
	}
}
