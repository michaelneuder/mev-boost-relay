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
	proposerInd  = uint64(987)
)

var (
	feeRecipient = types.Address{0x02}
	errFake      = fmt.Errorf("foo error")
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

func getTestBlockHash(t *testing.T) types.Hash {
	var blockHash types.Hash
	err := blockHash.FromSlice([]byte("98765432109876543210987654321098"))
	require.NoError(t, err)
	return blockHash
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
	err = backend.redis.SetKnownValidator(pubkey.PubkeyHex(), proposerInd)
	require.NoError(t, err)
	err = backend.redis.SaveExecutionPayload(
		slot,
		pkStr,
		getTestBlockHash(t).String(),
		&types.GetPayloadResponse{
			Data: &types.ExecutionPayload{
				Transactions: []hexutil.Bytes{},
			},
		},
	)
	require.NoError(t, err)
	err = backend.redis.SaveBidTrace(&common.BidTraceV2{
		BidTrace: types.BidTrace{
			Slot:           slot,
			ProposerPubkey: pubkey,
			BlockHash:      getTestBlockHash(t),
			BuilderPubkey:  pubkey,
		},
	})
	require.NoError(t, err)

	// Prepare db.
	err = backend.relay.db.SetBlockBuilderStatus(pkStr, common.Optimistic)
	require.NoError(t, err)

	// Prepare datastore.
	count, err := backend.relay.datastore.RefreshKnownValidators()
	require.NoError(t, err)
	require.Equal(t, count, 1)

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

	rr := backend.request(http.MethodPost, pathSubmitNewBlock, req)

	// Let updates happen async.
	time.Sleep(2 * time.Second)

	return rr
}

func runOptimisticGetPayload(t *testing.T, opts optimisticTestOpts, backend *testBackend) {
	var txn hexutil.Bytes
	err := txn.UnmarshalText([]byte("0x03"))
	require.NoError(t, err)

	backend.relay.blockSimRateLimiter = &MockBlockSimulationRateLimiter{
		simulationError: opts.simulationErr,
	}

	block := &types.BlindedBeaconBlock{
		Slot:          slot,
		ProposerIndex: proposerInd,
		Body: &types.BlindedBeaconBlockBody{
			ExecutionPayloadHeader: &types.ExecutionPayloadHeader{
				BlockHash:   getTestBlockHash(t),
				BlockNumber: 1234,
			},
			Eth1Data:      &types.Eth1Data{},
			SyncAggregate: &types.SyncAggregate{},
		},
	}
	signature, err := types.SignMessage(block, backend.relay.opts.EthNetDetails.DomainBeaconProposer, opts.secretkey)
	require.NoError(t, err)
	req := &types.SignedBlindedBeaconBlock{
		Message:   block,
		Signature: signature,
	}

	rr := backend.request(http.MethodPost, pathGetPayload, req)
	require.Equal(t, rr.Code, http.StatusOK)

	// Let updates happen async.
	time.Sleep(2 * time.Second)
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
			simulationErr:  errFake,
			expectDemotion: true,
			httpCode:       200, // success (in optimistic mode, block sim failure will happen async)
			blockValue:     types.IntToU256(uint64(collateral) - 1),
		},
		{
			description:    "failure_value_more_than_collateral",
			wantStatus:     common.Optimistic,
			simulationErr:  errFake,
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

			// Check demotion status is set to expected.
			mockDB := backend.relay.db.(*database.MockDB)
			require.Equal(t, mockDB.Demotions[pkStr], tc.expectDemotion)
		})
	}
}

func TestProposerApiGetPayloadOptimistic(t *testing.T) {
	testCases := []struct {
		description   string
		wantStatus    common.BuilderStatus
		simulationErr error
		expectRefund  bool
	}{
		{
			description:   "success",
			wantStatus:    common.Optimistic,
			simulationErr: nil,
			expectRefund:  false,
		},
		{
			description:   "sim_error_refund",
			wantStatus:    common.LowPrio,
			simulationErr: errFake,
			expectRefund:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			pk, sk, backend := startTestBackend(t)
			pkStr := pk.String()
			runOptimisticGetPayload(t, optimisticTestOpts{
				secretkey:     sk,
				pubkey:        pk,
				simulationErr: tc.simulationErr,
			}, backend)

			// Check status in redis.
			outStatus, err := backend.redis.GetBuilderStatus(pkStr)
			require.NoError(t, err)
			require.Equal(t, outStatus, tc.wantStatus)

			// Check status in db.
			dbBuilder, err := backend.relay.db.GetBlockBuilderByPubkey(pkStr)
			require.NoError(t, err)
			require.Equal(t, common.BuilderStatus(dbBuilder.Status), tc.wantStatus)

			// Check demotion and refund statuses are set to expected.
			mockDB := backend.relay.db.(*database.MockDB)
			require.Equal(t, mockDB.Demotions[pkStr], tc.expectRefund)
			require.Equal(t, mockDB.Refunds[pkStr], tc.expectRefund)
		})
	}
}
