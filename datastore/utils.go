package datastore

func MakeBlockBuilderStatus(c BlockBuilderStatusCode) BlockBuilderStatus {
	switch c {
	case HighPrio:
		return RedisBlockBuilderStatusHighPrio
	case Optimistic:
		return RedisBlockBuilderStatusOptimistic
	case Blacklisted:
		return RedisBlockBuilderStatusBlacklisted
	default:
		return RedisBlockBuilderStatusLowPrio
	}
}
