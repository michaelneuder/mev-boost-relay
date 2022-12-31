package datastore

import "github.com/flashbots/mev-boost-relay/common"

func MakeBlockBuilderStatus(c common.BlockBuilderStatusCode) BlockBuilderStatus {
	switch c {
	case common.HighPrio:
		return RedisBlockBuilderStatusHighPrio
	case common.Optimistic:
		return RedisBlockBuilderStatusOptimistic
	case common.Blacklisted:
		return RedisBlockBuilderStatusBlacklisted
	default:
		return RedisBlockBuilderStatusLowPrio
	}
}
