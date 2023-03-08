package beaconclient

import (
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/flashbots/go-boost-utils/types"
	"github.com/flashbots/mev-boost-relay/common"
)

type MockMultiBeaconClient struct{}

func NewMockMultiBeaconClient() *MockMultiBeaconClient {
	return &MockMultiBeaconClient{}
}

func (*MockMultiBeaconClient) BestSyncStatus() (*SyncStatusPayloadData, error) {
	return &SyncStatusPayloadData{HeadSlot: 1}, nil //nolint:exhaustruct
}

func (*MockMultiBeaconClient) SubscribeToHeadEvents(slotC chan HeadEventData) {}

func (*MockMultiBeaconClient) FetchValidators(headSlot uint64) (map[types.PubkeyHex]ValidatorResponseEntry, error) {
	return nil, nil
}

func (*MockMultiBeaconClient) GetProposerDuties(epoch uint64) (*ProposerDutiesResponse, error) {
	return nil, nil
}

func (*MockMultiBeaconClient) PublishBlock(block *common.SignedBeaconBlock) (code int, err error) {
	return 0, nil
}

func (*MockMultiBeaconClient) GetGenesis() (*GetGenesisResponse, error) {
	resp := &GetGenesisResponse{} //nolint:exhaustruct
	resp.Data.GenesisTime = 0
	return resp, nil
}

func (*MockMultiBeaconClient) GetSpec() (spec *GetSpecResponse, err error) {
	return nil, nil
}

func (*MockMultiBeaconClient) GetForkSchedule() (spec *GetForkScheduleResponse, err error) {
	resp := &GetForkScheduleResponse{}
	return resp, nil
}

func (*MockMultiBeaconClient) GetBlock(blockID string) (block *GetBlockResponse, err error) {
	return nil, nil
}

func (*MockMultiBeaconClient) GetRandao(slot uint64) (spec *GetRandaoResponse, err error) {
	return nil, nil
}

func (*MockMultiBeaconClient) GetWithdrawals(slot uint64) (spec *GetWithdrawalsResponse, err error) {
	resp := &GetWithdrawalsResponse{}
	resp.Data.Withdrawals = append(resp.Data.Withdrawals, &capella.Withdrawal{})
	return resp, nil
}
