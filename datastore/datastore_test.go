package datastore

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/flashbots/go-boost-utils/types"
	"github.com/flashbots/mev-boost-relay/common"
	"github.com/flashbots/mev-boost-relay/database"
	"github.com/jinzhu/copier"
	"github.com/stretchr/testify/require"
)

func setupTestDatastore(t *testing.T) *Datastore {
	t.Helper()
	var err error

	redisTestServer, err := miniredis.Run()
	require.NoError(t, err)

	redisDs, err := NewRedisCache(redisTestServer.Addr(), "")
	require.NoError(t, err)

	ds, err := NewDatastore(common.TestLog, redisDs, database.MockDB{})
	require.NoError(t, err)

	return ds
}

func TestProdProposerValidatorRegistration(t *testing.T) {
	ds := setupTestDatastore(t)

	var reg1 types.SignedValidatorRegistration
	err := copier.Copy(&reg1, &common.ValidPayloadRegisterValidator)
	require.NoError(t, err)

	key := types.NewPubkeyHex(reg1.Message.Pubkey.String())

	// Set known validator and save registration
	err = ds.redis.SetKnownValidator(key, 1)
	require.NoError(t, err)

	// Check if validator is known
	cnt, err := ds.RefreshKnownValidators()
	require.NoError(t, err)
	require.Equal(t, 1, cnt)
	require.True(t, ds.IsKnownValidator(key))

	// Copy the original registration
	var reg2 types.SignedValidatorRegistration
	err = copier.Copy(&reg2, &reg1)
	require.NoError(t, err)
}

func TestSetBlockBuilderStatusByCollateralID(t *testing.T) {
	ds := setupTestDatastore(t)
	mockDB := database.MockDB{
		Builders: map[string]*database.BlockBuilderEntry{
			"0xaceface": {
				BuilderPubkey: "0xaceface",
				CollateralID:  "id1",
				Status:        uint8(common.OptimisticActive),
			},
			"0xbadcafe": {
				BuilderPubkey: "0xbadcafe",
				CollateralID:  "id1",
				Status:        uint8(common.OptimisticActive),
			},
			"0xdeadbeef": {
				BuilderPubkey: "0xdeadbeef",
				CollateralID:  "id2",
				Status:        uint8(common.OptimisticActive),
			},
		},
	}
	ds.db = mockDB
	errs := ds.SetBlockBuilderStatusByCollateralID("0xaceface", common.OptimisticDemoted)
	require.Empty(t, errs)

	// Check redis & db are updated.
	for _, pubkey := range []string{"0xaceface", "0xbadcafe"} {
		status, err := ds.redis.GetBlockBuilderStatus(pubkey)
		require.NoError(t, err)
		require.Equal(t, status, common.OptimisticDemoted)

		status, err = ds.db.GetBlockBuilderStatus(pubkey)
		require.NoError(t, err)
		require.Equal(t, status, common.OptimisticDemoted)
	}
}
