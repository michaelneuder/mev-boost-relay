// Package migrations contains all the migration files
package migrations

import (
	migrate "github.com/rubenv/sql-migrate"
)

var Migrations = migrate.MemoryMigrationSource{
	Migrations: []*migrate.Migration{
		Migration001InitDatabase,
		Migration002RemoveIsBestAddReceivedAt,
		Migration003Optimistic,
		Migration004Temp,
		Migration005Profiling,
		Migration006ProfilingExt,
		Migration007Unzip,
		Migration008ProposerCommit,
		Migration009DemotionRefactor,
		Migration010Read,
		Migration011BidEligible,
	},
}
