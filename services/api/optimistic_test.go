package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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
	collateralID = "builder0x69"
	randao       = "01234567890123456789012345678901"
	proposerInd  = uint64(987)
)

var (
	feeRecipient = types.Address{0x02}
	errFake      = fmt.Errorf("foo error")
)

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

type blockRequestOpts struct {
	pubkey     types.PublicKey
	secretkey  *blst.SecretKey
	blockValue types.U256Str
	domain     types.Domain
}

func getTestBuilderSubmitBlockRequest(t *testing.T, opts blockRequestOpts) types.BuilderSubmitBlockRequest {
	var txn hexutil.Bytes
	err := txn.UnmarshalText([]byte("0x03"))
	require.NoError(t, err)
	bidTrace := &types.BidTrace{
		Slot:                 slot,
		BuilderPubkey:        opts.pubkey,
		ProposerFeeRecipient: feeRecipient,
		Value:                opts.blockValue,
	}
	signature, err := types.SignMessage(bidTrace, opts.domain, opts.secretkey)
	require.NoError(t, err)
	return types.BuilderSubmitBlockRequest{
		Message:   bidTrace,
		Signature: signature,
		ExecutionPayload: &types.ExecutionPayload{
			Timestamp:    slot * 12, // 12 seconds per slot.
			Transactions: []hexutil.Bytes{txn},
			Random:       getTestRandomHash(t),
		},
	}
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
	backend.relay.blockBuildersCache = map[string]*blockBuilderCacheEntry{
		pkStr: {
			status: common.BuilderStatus{
				IsHighPrio: true,
			},
			collateral: types.IntToU256(uint64(collateral)),
		},
	}

	// Setup test db, redis, and datastore.
	mockDB := &database.MockDB{
		Builders: map[string]*database.BlockBuilderEntry{
			pkStr: &database.BlockBuilderEntry{
				BuilderPubkey:   pkStr,
				IsHighPrio:      true,
				CollateralID:    collateralID,
				CollateralValue: strconv.Itoa(collateral),
			},
		},
		Demotions: map[string]bool{},
		Refunds:   map[string]bool{},
	}
	redisTestServer, err := miniredis.Run()
	require.NoError(t, err)
	mockRedis, err := datastore.NewRedisCache(redisTestServer.Addr(), "")
	require.NoError(t, err)
	mockDS, err := datastore.NewDatastore(backend.relay.log, mockRedis, mockDB)
	require.NoError(t, err)

	backend.relay.datastore = mockDS
	backend.relay.redis = mockRedis
	backend.relay.db = mockDB

	// Prepare redis.
	err = backend.relay.redis.SetStats(datastore.RedisStatsFieldSlotLastPayloadDelivered, slot-1)
	require.NoError(t, err)
	err = backend.relay.redis.SetKnownValidator(pubkey.PubkeyHex(), proposerInd)
	require.NoError(t, err)
	err = backend.relay.redis.SaveExecutionPayload(
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
	err = backend.relay.redis.SaveBidTrace(&common.BidTraceV2{
		BidTrace: types.BidTrace{
			Slot:           slot,
			ProposerPubkey: pubkey,
			BlockHash:      getTestBlockHash(t),
			BuilderPubkey:  pubkey,
		},
	})
	require.NoError(t, err)

	count, err := backend.relay.datastore.RefreshKnownValidators()
	require.NoError(t, err)
	require.Equal(t, count, 1)

	go backend.relay.StartServer()
	time.Sleep(100 * time.Millisecond)

	return pubkey, sk, backend
}

func runOptimisticGetPayload(t *testing.T, opts blockRequestOpts, simErr error, backend *testBackend) {
	var txn hexutil.Bytes
	err := txn.UnmarshalText([]byte("0x03"))
	require.NoError(t, err)

	backend.relay.blockSimRateLimiter = &MockBlockSimulationRateLimiter{
		simulationError: simErr,
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
	signature, err := types.SignMessage(block, opts.domain, opts.secretkey)
	require.NoError(t, err)
	req := &types.SignedBlindedBeaconBlock{
		Message:   block,
		Signature: signature,
	}

	rr := backend.request(http.MethodPost, pathGetPayload, req)
	require.Equal(t, rr.Code, http.StatusOK)

	// Let updates happen async.
	time.Sleep(100 * time.Millisecond)
}

func runOptimisticBlockSubmission(t *testing.T, opts blockRequestOpts, simErr error, backend *testBackend) *httptest.ResponseRecorder {
	var txn hexutil.Bytes
	err := txn.UnmarshalText([]byte("0x03"))
	require.NoError(t, err)

	backend.relay.blockSimRateLimiter = &MockBlockSimulationRateLimiter{
		simulationError: simErr,
	}

	// Set up request.
	bidTrace := &types.BidTrace{
		Slot:                 slot,
		BuilderPubkey:        opts.pubkey,
		ProposerFeeRecipient: feeRecipient,
		Value:                opts.blockValue,
	}
	signature, err := types.SignMessage(bidTrace, opts.domain, opts.secretkey)
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
	time.Sleep(100 * time.Millisecond)
	return rr
}

func TestSimulateBlock(t *testing.T) {
	cases := []struct {
		description     string
		simulationError error
	}{
		{
			description: "success",
		},
		{
			description:     "simulation_error",
			simulationError: errFake,
		},
	}
	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			pubkey, secretkey, backend := startTestBackend(t)
			backend.relay.blockSimRateLimiter = &MockBlockSimulationRateLimiter{
				simulationError: tc.simulationError,
			}
			err := backend.relay.simulateBlock(blockSimOptions{
				ctx:        context.Background(),
				isHighPrio: true,
				log:        backend.relay.log,
				req: &BuilderBlockValidationRequest{
					BuilderSubmitBlockRequest: getTestBuilderSubmitBlockRequest(t, blockRequestOpts{
						pubkey:     pubkey,
						secretkey:  secretkey,
						blockValue: types.IntToU256(uint64(collateral)),
						domain:     backend.relay.opts.EthNetDetails.DomainBuilder,
					}),
				},
			})
			require.Equal(t, tc.simulationError, err)
		})
	}
}

func TestProcessOptimisticBlock(t *testing.T) {
	cases := []struct {
		description     string
		wantStatus      common.BuilderStatus
		simulationError error
	}{
		{
			description: "success",
			wantStatus: common.BuilderStatus{
				IsDemoted:  false,
				IsHighPrio: true,
			},
		},
		{
			description: "simulation_error",
			wantStatus: common.BuilderStatus{
				IsDemoted:  true,
				IsHighPrio: true,
			},
			simulationError: errFake,
		},
	}
	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			pubkey, secretkey, backend := startTestBackend(t)
			pkStr := pubkey.String()
			backend.relay.blockSimRateLimiter = &MockBlockSimulationRateLimiter{
				simulationError: tc.simulationError,
			}
			backend.relay.processOptimisticBlock(blockSimOptions{
				ctx:        context.Background(),
				isHighPrio: true,
				log:        backend.relay.log,
				req: &BuilderBlockValidationRequest{
					BuilderSubmitBlockRequest: getTestBuilderSubmitBlockRequest(t, blockRequestOpts{
						pubkey:     pubkey,
						secretkey:  secretkey,
						blockValue: types.IntToU256(uint64(collateral)),
						domain:     backend.relay.opts.EthNetDetails.DomainBuilder,
					}),
				},
			})

			// Check status in db.
			builder, err := backend.relay.db.GetBlockBuilderByPubkey(pkStr)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus.IsDemoted, builder.IsDemoted)
			require.Equal(t, tc.wantStatus.IsHighPrio, builder.IsHighPrio)

			// Check demotion but no refund.
			if tc.simulationError != nil {
				mockDB := backend.relay.db.(*database.MockDB)
				require.True(t, mockDB.Demotions[pkStr])
				require.False(t, mockDB.Refunds[pkStr])
			}
		})
	}
}

func TestDemoteBuilder(t *testing.T) {
	cases := []struct {
		description string
		wantStatus  common.BuilderStatus
		wantRefund  bool
		block       *types.SignedBeaconBlock
		reg         *types.SignedValidatorRegistration
	}{
		{
			description: "no_refund",
			wantStatus: common.BuilderStatus{
				IsDemoted:  true,
				IsHighPrio: true,
			},
			wantRefund: false,
		},
		{
			description: "refund",
			wantStatus: common.BuilderStatus{
				IsDemoted:  true,
				IsHighPrio: true,
			},
			wantRefund: true,
			block:      &types.SignedBeaconBlock{},
			reg:        &types.SignedValidatorRegistration{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			pubkey, secretkey, backend := startTestBackend(t)
			pkStr := pubkey.String()
			req := getTestBuilderSubmitBlockRequest(t, blockRequestOpts{
				pubkey:     pubkey,
				secretkey:  secretkey,
				blockValue: types.IntToU256(uint64(collateral)),
				domain:     backend.relay.opts.EthNetDetails.DomainBuilder,
			})
			backend.relay.demoteBuilder(pkStr, &req, tc.block, tc.reg)

			// Check status in db.
			builder, err := backend.relay.db.GetBlockBuilderByPubkey(pkStr)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus.IsDemoted, builder.IsDemoted)
			require.Equal(t, tc.wantStatus.IsHighPrio, builder.IsHighPrio)

			// Check demotion and refund statuses.
			mockDB := backend.relay.db.(*database.MockDB)
			require.True(t, mockDB.Demotions[pkStr])
			require.Equal(t, tc.wantRefund, mockDB.Refunds[pkStr])
		})
	}
}

func TestUpdateOptimisticSlot(t *testing.T) {
	pubkey, _, backend := startTestBackend(t)
	pkStr := pubkey.String()
	// Clear cache.
	backend.relay.blockBuildersCache = map[string]*blockBuilderCacheEntry{}
	backend.relay.updateOptimisticSlot(slot + 1)
	entry, ok := backend.relay.blockBuildersCache[pkStr]
	require.True(t, ok)
	require.Equal(t, true, entry.status.IsHighPrio)
	require.Equal(t, false, entry.status.IsDemoted)
	require.Equal(t, false, entry.status.IsBlacklisted)
	require.Equal(t, types.IntToU256(uint64(collateral)), entry.collateral)
}

func TestProposerApiGetPayloadOptimistic(t *testing.T) {
	testCases := []struct {
		description     string
		wantStatus      common.BuilderStatus
		simulationError error
	}{
		{
			description: "success",
			wantStatus: common.BuilderStatus{
				IsDemoted:  false,
				IsHighPrio: true,
			},
			simulationError: nil,
		},
		{
			description: "sim_error_refund",
			wantStatus: common.BuilderStatus{
				IsDemoted:  true,
				IsHighPrio: true,
			},
			simulationError: errFake,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			pubkey, secretkey, backend := startTestBackend(t)
			pkStr := pubkey.String()
			runOptimisticGetPayload(t, blockRequestOpts{
				secretkey: secretkey,
				pubkey:    pubkey,
				domain:    backend.relay.opts.EthNetDetails.DomainBeaconProposer,
			}, tc.simulationError, backend)

			// Check status in db.
			builder, err := backend.relay.db.GetBlockBuilderByPubkey(pkStr)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus.IsDemoted, builder.IsDemoted)
			require.Equal(t, tc.wantStatus.IsHighPrio, builder.IsHighPrio)

			// Check demotion and refund.
			if tc.simulationError != nil {
				mockDB := backend.relay.db.(*database.MockDB)
				require.True(t, mockDB.Demotions[pkStr])
				require.True(t, mockDB.Refunds[pkStr])
			}
		})
	}
}

func TestBuilderApiSubmitNewBlockOptimistic(t *testing.T) {
	testCases := []struct {
		description     string
		wantStatus      common.BuilderStatus
		simulationError error
		expectDemotion  bool
		httpCode        uint64
		blockValue      types.U256Str
	}{
		{
			description: "success_value_less_than_collateral",
			wantStatus: common.BuilderStatus{
				IsDemoted:  false,
				IsHighPrio: true,
			},
			simulationError: nil,
			expectDemotion:  false,
			httpCode:        200, // success
			blockValue:      types.IntToU256(uint64(collateral) - 1),
		},
		{
			description: "success_value_greater_than_collateral",
			wantStatus: common.BuilderStatus{
				IsDemoted:  false,
				IsHighPrio: true,
			},
			simulationError: nil,
			expectDemotion:  false,
			httpCode:        200, // success
			blockValue:      types.IntToU256(uint64(collateral) + 1),
		},
		{
			description: "failure_value_less_than_collateral",
			wantStatus: common.BuilderStatus{
				IsDemoted:  true,
				IsHighPrio: true,
			},
			simulationError: errFake,
			expectDemotion:  true,
			httpCode:        200, // success (in optimistic mode, block sim failure will happen async)
			blockValue:      types.IntToU256(uint64(collateral) - 1),
		},
		{
			description: "failure_value_more_than_collateral",
			wantStatus: common.BuilderStatus{
				IsDemoted:  false,
				IsHighPrio: true,
			},
			simulationError: errFake,
			expectDemotion:  false,
			httpCode:        400, // failure (in pessimistic mode, block sim failure happens in response path)
			blockValue:      types.IntToU256(uint64(collateral) + 1),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			pubkey, secretkey, backend := startTestBackend(t)
			backend.relay.optimisticSlot = slot
			pkStr := pubkey.String()
			rr := runOptimisticBlockSubmission(t, blockRequestOpts{
				secretkey:  secretkey,
				pubkey:     pubkey,
				blockValue: tc.blockValue,
				domain:     backend.relay.opts.EthNetDetails.DomainBuilder,
			}, tc.simulationError, backend)

			// Check http code.
			require.Equal(t, uint64(rr.Code), tc.httpCode)

			// Check status in db.
			builder, err := backend.relay.db.GetBlockBuilderByPubkey(pkStr)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus.IsDemoted, builder.IsDemoted)
			require.Equal(t, tc.wantStatus.IsHighPrio, builder.IsHighPrio)

			// Check demotion status is set to expected and refund is false.
			mockDB := backend.relay.db.(*database.MockDB)
			require.Equal(t, mockDB.Demotions[pkStr], tc.expectDemotion)
			require.False(t, mockDB.Refunds[pkStr])
		})
	}
}

func TestInternalBuilderStatus(t *testing.T) {
	pubkey, _, backend := startTestBackend(t)
	path := "/internal/v1/builder/" + pubkey.String()

	setAndGetStatus := func(arg string, expected common.BuilderStatus) {
		// Set & Get.
		rr := backend.request(http.MethodPost, path+arg, nil)
		require.Equal(t, rr.Code, http.StatusOK)

		rr = backend.request(http.MethodGet, path, nil)
		require.Equal(t, rr.Code, http.StatusOK)
		resp := &database.BlockBuilderEntry{}
		err := json.Unmarshal(rr.Body.Bytes(), &resp)
		require.NoError(t, err)
		require.Equal(t, expected.IsHighPrio, resp.IsHighPrio)
		require.Equal(t, expected.IsBlacklisted, resp.IsBlacklisted)
		require.Equal(t, expected.IsDemoted, resp.IsDemoted)
	}
	setAndGetStatus("?high_prio=true", common.BuilderStatus{IsHighPrio: true})
	setAndGetStatus("?blacklisted=true", common.BuilderStatus{IsBlacklisted: true})
	setAndGetStatus("?demoted=true", common.BuilderStatus{IsDemoted: true})
	setAndGetStatus("", common.BuilderStatus{})
}

func TestInternalBuilderCollateral(t *testing.T) {
	pubkey, _, backend := startTestBackend(t)
	path := "/internal/v1/builder/collateral/" + pubkey.String()

	// Set & Get.
	rr := backend.request(http.MethodPost, path+"?collateral_id=builder0x69&value=10000", nil)
	require.Equal(t, rr.Code, http.StatusOK)

	rr = backend.request(http.MethodGet, "/internal/v1/builder/"+pubkey.String(), nil)
	require.Equal(t, rr.Code, http.StatusOK)
	resp := &database.BlockBuilderEntry{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, resp.CollateralID, "builder0x69")
	require.Equal(t, resp.CollateralValue, "10000")
}
