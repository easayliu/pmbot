package clob

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Polymarket wallet factory addresses and init code hashes (Polygon mainnet).
var (
	// safeFactory is the Gnosis Safe Proxy Factory used for browser wallet (MetaMask) users.
	safeFactory = common.HexToAddress("0xaacFeEa03eb1561C4e67d661e40682Bd20E3541b")
	// safeInitCodeHash is the init code hash for Gnosis Safe proxy creation.
	safeInitCodeHash = common.HexToHash("0x2bce2127ff07fb632d16c8347c4ebf501f4841168bed00d9e6ef715ddb6fcecf")

	// proxyFactory is the Polymarket Proxy Factory used for Magic/email wallet users.
	proxyFactory = common.HexToAddress("0xaB45c5A4B0c941a2F231C04C3f49182e1A254052")
	// proxyInitCodeHash is the init code hash for EIP-1167 minimal proxy creation.
	proxyInitCodeHash = common.HexToHash("0xd21df8dc65880a8606f09fe0ce3df9b8869287ab0b058be05aa9e8af6330a00b")
)

// DeriveSafeAddress derives the Polymarket Gnosis Safe wallet address from an EOA.
// This is used for browser wallet (MetaMask) users (SignatureTypeEOA).
func DeriveSafeAddress(eoa common.Address) common.Address {
	// Salt: keccak256(abi.encode(address)) — address left-padded to 32 bytes.
	var padded [32]byte
	copy(padded[12:], eoa.Bytes())
	salt := crypto.Keccak256Hash(padded[:])

	// CREATE2: keccak256(0xff ++ factory ++ salt ++ init_code_hash)[12:]
	return crypto.CreateAddress2(safeFactory, salt, safeInitCodeHash.Bytes())
}

// DeriveProxyAddress derives the Polymarket Proxy wallet address from an EOA.
// This is used for Magic/email wallet users (SignatureTypePolyProxy).
func DeriveProxyAddress(eoa common.Address) common.Address {
	// Salt: keccak256(encodePacked(address)) — raw 20-byte address, no padding.
	salt := crypto.Keccak256Hash(eoa.Bytes())

	// CREATE2: keccak256(0xff ++ factory ++ salt ++ init_code_hash)[12:]
	return crypto.CreateAddress2(proxyFactory, salt, proxyInitCodeHash.Bytes())
}

// DerivePolymarketAddress derives the appropriate Polymarket wallet address
// based on the wallet signature type.
func DerivePolymarketAddress(eoa common.Address, sigType SignatureType) common.Address {
	switch sigType {
	case SignatureTypePolyProxy:
		return DeriveProxyAddress(eoa)
	default:
		return DeriveSafeAddress(eoa)
	}
}
