// Package datastore helps storing data, utilizing Redis and Postgres as backends
package datastore

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/flashbots/go-boost-utils/types"
	"github.com/flashbots/mev-boost-relay/common"
	"github.com/flashbots/mev-boost-relay/database"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type GetHeaderResponseKey struct {
	Slot           uint64
	ParentHash     string
	ProposerPubkey string
}

type GetPayloadResponseKey struct {
	Slot           uint64
	ProposerPubkey string
	BlockHash      string
}

// Datastore provides a local memory cache with a Redis and DB backend
type Datastore struct {
	log *logrus.Entry

	redis *RedisCache
	db    database.IDatabaseService

	knownValidatorsByPubkey map[types.PubkeyHex]uint64
	knownValidatorsByIndex  map[uint64]types.PubkeyHex
	knownValidatorsLock     sync.RWMutex
}

func NewDatastore(log *logrus.Entry, redisCache *RedisCache, db database.IDatabaseService) (ds *Datastore, err error) {
	ds = &Datastore{
		log:                     log.WithField("component", "datastore"),
		db:                      db,
		redis:                   redisCache,
		knownValidatorsByPubkey: make(map[types.PubkeyHex]uint64),
		knownValidatorsByIndex:  make(map[uint64]types.PubkeyHex),
	}

	return ds, err
}

// RefreshKnownValidators loads known validators from Redis into memory
func (ds *Datastore) RefreshKnownValidators() (cnt int, err error) {
	knownValidators, err := ds.redis.GetKnownValidators()
	if err != nil {
		return 0, err
	}

	knownValidatorsByIndex := make(map[uint64]types.PubkeyHex)
	for pubkey, index := range knownValidators {
		knownValidatorsByIndex[index] = pubkey
	}

	ds.knownValidatorsLock.Lock()
	defer ds.knownValidatorsLock.Unlock()
	ds.knownValidatorsByPubkey = knownValidators
	ds.knownValidatorsByIndex = knownValidatorsByIndex
	return len(knownValidators), nil
}

func (ds *Datastore) IsKnownValidator(pubkeyHex types.PubkeyHex) bool {
	ds.knownValidatorsLock.RLock()
	defer ds.knownValidatorsLock.RUnlock()
	_, found := ds.knownValidatorsByPubkey[pubkeyHex]
	return found
}

func (ds *Datastore) GetKnownValidatorPubkeyByIndex(index uint64) (types.PubkeyHex, bool) {
	ds.knownValidatorsLock.RLock()
	defer ds.knownValidatorsLock.RUnlock()
	pk, found := ds.knownValidatorsByIndex[index]
	return pk, found
}

func (ds *Datastore) NumKnownValidators() int {
	ds.knownValidatorsLock.RLock()
	defer ds.knownValidatorsLock.RUnlock()
	return len(ds.knownValidatorsByIndex)
}

func (ds *Datastore) NumRegisteredValidators() (uint64, error) {
	return ds.db.NumRegisteredValidators()
}

// SaveValidatorRegistration saves a validator registration into both Redis and the database
func (ds *Datastore) SaveValidatorRegistration(entry types.SignedValidatorRegistration) error {
	// First save in the database
	err := ds.db.SaveValidatorRegistration(database.SignedValidatorRegistrationToEntry(entry))
	if err != nil {
		return errors.Wrap(err, "failed saving validator registration to database")
	}

	// then save in redis
	pk := types.NewPubkeyHex(entry.Message.Pubkey.String())
	err = ds.redis.SetValidatorRegistrationTimestampIfNewer(pk, entry.Message.Timestamp)
	if err != nil {
		return errors.Wrap(err, "failed saving validator registration to redis")
	}

	return nil
}

// GetGetPayloadResponse returns the getPayload response from memory or Redis or Database
func (ds *Datastore) GetGetPayloadResponse(slot uint64, proposerPubkey, blockHash string) (*types.GetPayloadResponse, error) {
	_proposerPubkey := strings.ToLower(proposerPubkey)
	_blockHash := strings.ToLower(blockHash)

	// 1. try to get from Redis
	resp, err := ds.redis.GetExecutionPayload(slot, _proposerPubkey, _blockHash)
	if err != nil {
		ds.log.WithError(err).Error("error getting getPayload response from redis")
	} else {
		ds.log.Debug("getPayload response from redis")
		return resp, nil
	}

	// 2. try to get from database
	blockSubEntry, err := ds.db.GetExecutionPayloadEntryBySlotPkHash(slot, proposerPubkey, blockHash)
	if err != nil {
		return nil, err
	}

	// deserialize execution payload
	executionPayload := new(types.ExecutionPayload)
	err = json.Unmarshal([]byte(blockSubEntry.Payload), executionPayload)
	if err != nil {
		return nil, err
	}

	ds.log.Debug("getPayload response from database")
	return &types.GetPayloadResponse{
		Version: types.VersionString(blockSubEntry.Version),
		Data:    executionPayload,
	}, nil
}

// SetBlockBuilderStatusByCollateralID modifies the status of a set of builders
// who have matching collateral IDs. This function continues to try change
// the statuses, even if some fail. The returned slice contains all the errors
// encountered.
func (ds *Datastore) SetBlockBuilderStatusByCollateralID(builderPubkey string, status common.BuilderStatus) []error {
	// Fetch builder collateral_id.
	builder, err := ds.db.GetBlockBuilderByPubkey(builderPubkey)
	if err != nil {
		err = fmt.Errorf("unable to get builder from database: %v", err)
		ds.log.Error(err.Error())
		return []error{err}
	}

	// Fetch all builder pubkeys using the collateral_id.
	builderPubkeys, err := ds.db.GetBlockBuilderPubkeysByCollateralID(builder.CollateralID)
	if err != nil {
		err = fmt.Errorf("unable to get builder pubkeys by collateral id: %v", err)
		ds.log.Error(err.Error())
		return []error{err}
	}

	var errs []error
	for _, pubkey := range builderPubkeys {
		err := ds.db.SetBlockBuilderStatus(pubkey, status)
		if err != nil {
			err = fmt.Errorf("failed to set block builder: %v status in db: %v", pubkey, err)
			ds.log.Error(err.Error())
			errs = append(errs, err)
		}
	}
	return errs
}
