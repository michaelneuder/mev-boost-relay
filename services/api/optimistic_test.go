package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flashbots/go-boost-utils/bls"
	"github.com/flashbots/go-boost-utils/types"
	"github.com/flashbots/mev-boost-relay/beaconclient"
	"github.com/flashbots/mev-boost-relay/common"
	"github.com/flashbots/mev-boost-relay/database"
	"github.com/flashbots/mev-boost-relay/datastore"
	"github.com/stretchr/testify/require"
	blst "github.com/supranational/blst/bindings/go"
)

const (
	slot         = uint64(42)
	collateral   = 1000
	collateralID = 567
	randao       = "01234567890123456789012345678901"
	path         = "/relay/v1/builder/blocks"
)

var (
	feeRecipient = types.Address{0x02}
)

type optimisticTestOpts struct {
	pubkey        types.PublicKey
	secretkey     *blst.SecretKey
	simulationErr error
	blockValue    types.U256Str
}

func getTestRandomHash(t *testing.T) types.Hash {
	var random types.Hash
	err := random.FromSlice([]byte(randao))
	require.NoError(t, err)
	return random
}

func startTestBackend(t *testing.T) (types.PublicKey, *blst.SecretKey, *testBackend) {
	// Setup test key pair.
	sk, _, err := bls.GenerateNewKeypair()
	require.NoError(t, err)
	blsPubkey := bls.PublicKeyFromSecretKey(sk)
	var pubkey types.PublicKey
	err = pubkey.FromSlice(blsPubkey.Compress())
	require.NoError(t, err)
	pkStr := pubkey.String()

	// Setup test backend.
	backend := newTestBackend(t, 1)
	backend.relay.expectedPrevRandao = randaoHelper{
		slot:       slot,
		prevRandao: getTestRandomHash(t).String(),
	}
	backend.relay.genesisInfo = &beaconclient.GetGenesisResponse{}
	backend.relay.genesisInfo.Data.GenesisTime = 0
	backend.relay.proposerDutiesMap = map[uint64]*types.RegisterValidatorRequestMessage{
		slot: &types.RegisterValidatorRequestMessage{
			FeeRecipient: feeRecipient,
			GasLimit:     5000,
			Timestamp:    0xffffffff,
			Pubkey:       types.PublicKey{},
		},
	}
	backend.relay.opts.BlockBuilderAPI = true
	backend.relay.beaconClient = beaconclient.NewMockMultiBeaconClient()
	backend.relay.blockSimRateLimiter = &MockBlockSimulationRateLimiter{}
	backend.relay.db = &database.MockDB{
		Builders: map[string]*database.BlockBuilderEntry{
			pkStr: &database.BlockBuilderEntry{
				BuilderPubkey: pkStr,
				CollateralID:  collateralID,
			},
		},
		Demotions: map[string]bool{},
		Refunds:   map[string]bool{},
	}
	go backend.relay.StartServer()
	time.Sleep(1 * time.Second)

	// Prepare redis.
	err = backend.redis.SetStats(datastore.RedisStatsFieldSlotLastPayloadDelivered, slot-1)
	require.NoError(t, err)
	err = backend.redis.SetBuilderStatus(pkStr, common.Optimistic)
	require.NoError(t, err)
	err = backend.redis.SetBuilderCollateral(pkStr, strconv.Itoa(collateral))
	require.NoError(t, err)

	// Prepare db.
	err = backend.relay.db.SetBlockBuilderStatus(pkStr, common.Optimistic)
	require.NoError(t, err)

	return pubkey, sk, backend
}

func runOptimisticBlockSubmission(t *testing.T, opts optimisticTestOpts, backend *testBackend) *httptest.ResponseRecorder {
	var txn hexutil.Bytes
	err := txn.UnmarshalText([]byte("0x03"))
	require.NoError(t, err)

	backend.relay.blockSimRateLimiter = &MockBlockSimulationRateLimiter{
		simulationError: opts.simulationErr,
	}

	// Set up request.
	bidTrace := &types.BidTrace{
		Slot:                 slot,
		BuilderPubkey:        opts.pubkey,
		ProposerFeeRecipient: feeRecipient,
		Value:                opts.blockValue,
	}
	signature, err := types.SignMessage(bidTrace, backend.relay.opts.EthNetDetails.DomainBuilder, opts.secretkey)
	require.NoError(t, err)
	req := &types.BuilderSubmitBlockRequest{
		Message:   bidTrace,
		Signature: signature,
		ExecutionPayload: &types.ExecutionPayload{
			Timestamp:    slot * 12, // 12 seconds per slot.
			Transactions: []hexutil.Bytes{txn},
			Random:       getTestRandomHash(t),
		},
	}

	rr := backend.request(http.MethodPost, path, req)

	// Let updates happen async.
	time.Sleep(2 * time.Second)

	return rr
}

func TestBuilderApiSubmitNewBlockOptimistic(t *testing.T) {
	testCases := []struct {
		description    string
		wantStatus     common.BuilderStatus
		simulationErr  error
		expectDemotion bool
		httpCode       uint64
		blockValue     types.U256Str
	}{
		{
			description:    "success_value_less_than_collateral",
			wantStatus:     common.Optimistic,
			simulationErr:  nil,
			expectDemotion: false,
			httpCode:       200, // success
			blockValue:     types.IntToU256(uint64(collateral) - 1),
		},
		{
			description:    "success_value_greater_than_collateral",
			wantStatus:     common.Optimistic,
			simulationErr:  nil,
			expectDemotion: false,
			httpCode:       200, // success
			blockValue:     types.IntToU256(uint64(collateral) + 1),
		},
		{
			description:    "failure_value_less_than_collateral",
			wantStatus:     common.LowPrio,
			simulationErr:  fmt.Errorf("fake error"),
			expectDemotion: true,
			httpCode:       200, // success (in optimistic mode, block sim failure will happen async)
			blockValue:     types.IntToU256(uint64(collateral) - 1),
		},
		{
			description:    "failure_value_more_than_collateral",
			wantStatus:     common.Optimistic,
			simulationErr:  fmt.Errorf("fake error"),
			expectDemotion: false,
			httpCode:       400, // failure (in pessimistic mode, block sim failure happens in response path)
			blockValue:     types.IntToU256(uint64(collateral) + 1),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			pk, sk, backend := startTestBackend(t)
			pkStr := pk.String()
			rr := runOptimisticBlockSubmission(t, optimisticTestOpts{
				secretkey:     sk,
				pubkey:        pk,
				simulationErr: tc.simulationErr,
				blockValue:    tc.blockValue,
			}, backend)

			// Check http code.
			require.Equal(t, uint64(rr.Code), tc.httpCode)

			// Check status in redis.
			outStatus, err := backend.redis.GetBuilderStatus(pkStr)
			require.NoError(t, err)
			require.Equal(t, outStatus, tc.wantStatus)

			// Check status in db.
			dbBuilder, err := backend.relay.db.GetBlockBuilderByPubkey(pkStr)
			require.NoError(t, err)
			require.Equal(t, common.BuilderStatus(dbBuilder.Status), tc.wantStatus)

			// Check demotion status is set to true.
			mockDB := backend.relay.db.(*database.MockDB)
			require.Equal(t, mockDB.Demotions[pkStr], tc.expectDemotion)
		})
	}
}
