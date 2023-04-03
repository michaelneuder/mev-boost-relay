package api

import (
	"encoding/json"
	"fmt"
	"testing"

	boostTypes "github.com/flashbots/go-boost-utils/types"
	"github.com/flashbots/mev-boost-relay/common"
	"github.com/stretchr/testify/require"
)

const (
	// from relay database.
	parentRoot1 = "0x0000000000000000000000000000000000000000000000000000000000000000"
	stateRoot1  = "0x0000000000000000000000000000000000000000000000000000000000000000"
	bodyRoot1   = "0x82710f9c7025617ac3ef1f722451c05046ea01d525d60b7594669e63080e5db7"
	signature1  = "0xb544eaa34654372143d0442fbba6713d978f780da85503b2a070998970867b4936e884c9f948d51c6e08f6c927f0cd4d0a334e4f04404876e08863e73d8add1b3949fd911eb386fa24ea4c1dc062564b0af8a2f95e8940737f958c8baa584cd6"

	// from https://beaconcha.in/slot/6137846#overview
	parentRoot2 = "0x873f73baa696664b8b73b160e7cfe352a924238935324007086e93a158b6e23d"
	stateRoot2  = "0xfad223f846ec0798c8a128e52ded70c02eeda3bdcf733db7ee77b0f02a46cef9"
	bodyRoot2   = "0xe8b27e1cb99c74d0a39d19a573743426aba74cca872b3fc44115113c669d5a96"
	signature2  = "0x90f6920c78ed00a06affa06231780b9ad632b54ee2e1a6531e3881c9a96f47dadfba648566cdd684386df19150a9b6eb18a11e79f3d71e5fc85c7af81dd5fcd92f8ddbc870e526caed49c48ebc426489abc1efc64e3c7a14541b558877309141"

	proposerPubkey = "0x960c2f877337b81561b7cfbfb22e4d60599a37aa3590b9f5f9dd3d363d22a964f02d4e8eb86704b06aa5f8516e34d199"
)

func TestSignature1(t *testing.T) {
	d, err := common.NewEthNetworkDetails("mainnet")
	require.Nil(t, err)

	var pRoot1, sRoot1, bRoot1 boostTypes.Hash
	err = pRoot1.UnmarshalText([]byte(parentRoot1))
	require.NoError(t, err)
	err = sRoot1.UnmarshalText([]byte(stateRoot1))
	require.NoError(t, err)
	err = bRoot1.UnmarshalText([]byte(bodyRoot1))
	require.NoError(t, err)

	var sig1 boostTypes.Signature
	err = sig1.UnmarshalText([]byte(signature1))
	require.NoError(t, err)

	h := &boostTypes.BeaconBlockHeader{
		Slot:          6137846,
		ProposerIndex: 552061,
		ParentRoot:    pRoot1,
		StateRoot:     sRoot1,
		BodyRoot:      bRoot1,
	}
	pk, err := boostTypes.HexToPubkey(proposerPubkey)
	require.Nil(t, err)

	ok, err := boostTypes.VerifySignature(h, d.DomainBeaconProposerBellatrix, pk[:], sig1[:])
	require.Nil(t, err)
	require.True(t, ok)
}

func TestSignature2(t *testing.T) {
	d, err := common.NewEthNetworkDetails("mainnet")
	require.Nil(t, err)

	// From https://beaconcha.in/slot/6137846#overview.
	var pRoot2, sRoot2, bRoot2 boostTypes.Hash
	err = pRoot2.UnmarshalText([]byte(parentRoot2))
	require.NoError(t, err)
	err = sRoot2.UnmarshalText([]byte(stateRoot2))
	require.NoError(t, err)
	err = bRoot2.UnmarshalText([]byte(bodyRoot2))
	require.NoError(t, err)

	var sig2 boostTypes.Signature
	err = sig2.UnmarshalText([]byte(signature2))
	require.NoError(t, err)

	h := &boostTypes.BeaconBlockHeader{
		Slot:          6137846,
		ProposerIndex: 552061,
		ParentRoot:    pRoot2,
		StateRoot:     sRoot2,
		BodyRoot:      bRoot2,
	}
	pk, err := boostTypes.HexToPubkey(proposerPubkey)
	require.Nil(t, err)

	ok, err := boostTypes.VerifySignature(h, d.DomainBeaconProposerBellatrix, pk[:], sig2[:])
	require.Nil(t, err)
	require.True(t, ok)
}

func TestConstructSlashing(t *testing.T) {
	var pRoot1, sRoot1, bRoot1 boostTypes.Hash
	err := pRoot1.UnmarshalText([]byte(parentRoot1))
	require.NoError(t, err)
	err = sRoot1.UnmarshalText([]byte(stateRoot1))
	require.NoError(t, err)
	err = bRoot1.UnmarshalText([]byte(bodyRoot1))
	require.NoError(t, err)

	var sig1 boostTypes.Signature
	err = sig1.UnmarshalText([]byte(signature1))
	require.NoError(t, err)

	// From https://beaconcha.in/slot/6137846#overview.
	var pRoot2, sRoot2, bRoot2 boostTypes.Hash
	err = pRoot2.UnmarshalText([]byte(parentRoot2))
	require.NoError(t, err)
	err = sRoot2.UnmarshalText([]byte(stateRoot2))
	require.NoError(t, err)
	err = bRoot2.UnmarshalText([]byte(bodyRoot2))
	require.NoError(t, err)

	var sig2 boostTypes.Signature
	err = sig2.UnmarshalText([]byte(signature2))
	require.NoError(t, err)

	slashing := boostTypes.ProposerSlashing{
		A: &boostTypes.SignedBeaconBlockHeader{
			Header: &boostTypes.BeaconBlockHeader{
				Slot:          6137846,
				ProposerIndex: 552061,
				ParentRoot:    pRoot1,
				StateRoot:     sRoot1,
				BodyRoot:      bRoot1,
			},
			Signature: sig1,
		},
		B: &boostTypes.SignedBeaconBlockHeader{
			Header: &boostTypes.BeaconBlockHeader{
				Slot:          6137846,
				ProposerIndex: 552061,
				ParentRoot:    pRoot2,
				StateRoot:     sRoot2,
				BodyRoot:      bRoot2,
			},
			Signature: sig2,
		},
	}

	out, err := json.MarshalIndent(slashing, "", " ")
	require.Nil(t, err)
	fmt.Printf("%v\n", string(out))
}
