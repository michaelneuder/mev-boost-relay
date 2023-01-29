package migrations

import (
	"github.com/flashbots/mev-boost-relay/database/vars"
	migrate "github.com/rubenv/sql-migrate"
)

var Migration003Optimistic = &migrate.Migration{
	Id: "003-optimistic",
	Up: []string{`
		ALTER TABLE ` + vars.TableBlockBuilder + ` ADD status            int;
		ALTER TABLE ` + vars.TableBlockBuilder + ` ADD collateral_value  NUMERIC(48, 0);
		ALTER TABLE ` + vars.TableBlockBuilder + ` ADD collateral_id     varchar(98);
	`,
		// Set all statuses to low-prio.
		`
		UPDATE ` + vars.TableBlockBuilder + `
			SET status = 0;
	`,
		// Set high-prio builder status.
		`
		UPDATE ` + vars.TableBlockBuilder + `
			SET status = 1
			WHERE is_high_prio = true;
	`,
		// Set blacklisted builder status.
		`
		UPDATE ` + vars.TableBlockBuilder + `
			SET status = 4
			WHERE is_blacklisted = true;
	`,
		// Drop is_high_prio and is_blacklisted.
		`
		ALTER TABLE ` + vars.TableBlockBuilder + ` DROP COLUMN is_high_prio;
		ALTER TABLE ` + vars.TableBlockBuilder + ` DROP COLUMN is_blacklisted;
	`,
		// Create builder demotion table.
		`
		CREATE TABLE IF NOT EXISTS ` + vars.TableBuilderDemotions + `(
			id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			inserted_at timestamp NOT NULL default current_timestamp,
			
			submit_block_request          json,
			signed_beacon_block  	      json,
			signed_validator_registration json,

			epoch bigint NOT NULL,
			slot  bigint NOT NULL,
			
			builder_pubkey  varchar(98) NOT NULL,
			proposer_pubkey varchar(98) NOT NULL,
			
			value NUMERIC(48, 0),
			
			fee_recipient varchar(42) NOT NULL,
			gas_limit     bigint NOT NULL,
			
			block_hash varchar(66) NOT NULL,
			
			UNIQUE (builder_pubkey, block_hash)
		);
	`},
	Down: []string{},

	DisableTransactionUp:   true,
	DisableTransactionDown: true,
}
