package clob

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// EIP-712 domain separator type hashes.
var (
	// domainTypeHashAuth = keccak256("EIP712Domain(string name,string version,uint256 chainId)")
	domainTypeHashAuth = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId)"))

	// domainTypeHashOrder = keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
	domainTypeHashOrder = crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))

	// clobAuthTypeHash = keccak256("ClobAuth(address address,string timestamp,uint256 nonce,string message)")
	clobAuthTypeHash = crypto.Keccak256Hash([]byte("ClobAuth(address address,string timestamp,uint256 nonce,string message)"))

	// orderTypeHash = keccak256("Order(uint256 salt,address maker,address signer,address taker,uint256 tokenId,uint256 makerAmount,uint256 takerAmount,uint256 expiration,uint256 nonce,uint256 feeRateBps,uint8 side,uint8 signatureType)")
	orderTypeHash = crypto.Keccak256Hash([]byte("Order(uint256 salt,address maker,address signer,address taker,uint256 tokenId,uint256 makerAmount,uint256 takerAmount,uint256 expiration,uint256 nonce,uint256 feeRateBps,uint8 side,uint8 signatureType)"))

	clobAuthMessage = "This message attests that I control the given wallet"
)

// Polymarket contract addresses on Polygon mainnet.
var (
	ExchangeAddress        = common.HexToAddress("0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E")
	NegRiskExchangeAddress = common.HexToAddress("0xC5d563A36AE78145C45a50134d48A1215220f80a")
)

// buildAuthDomainSeparator builds the EIP-712 domain separator for ClobAuth.
func buildAuthDomainSeparator(chainID int) common.Hash {
	return crypto.Keccak256Hash(
		domainTypeHashAuth.Bytes(),
		crypto.Keccak256([]byte("ClobAuthDomain")),
		crypto.Keccak256([]byte("1")),
		common.LeftPadBytes(big.NewInt(int64(chainID)).Bytes(), 32),
	)
}

// buildOrderDomainSeparator builds the EIP-712 domain separator for Order signing.
func buildOrderDomainSeparator(chainID int, exchange common.Address) common.Hash {
	return crypto.Keccak256Hash(
		domainTypeHashOrder.Bytes(),
		crypto.Keccak256([]byte("Polymarket CTF Exchange")),
		crypto.Keccak256([]byte("1")),
		common.LeftPadBytes(big.NewInt(int64(chainID)).Bytes(), 32),
		common.LeftPadBytes(exchange.Bytes(), 32),
	)
}

// signClobAuth signs a ClobAuth EIP-712 message for L1 authentication.
func signClobAuth(key *ecdsa.PrivateKey, address common.Address, timestamp string, nonce int, chainID int) (string, error) {
	domainSep := buildAuthDomainSeparator(chainID)

	structHash := crypto.Keccak256Hash(
		clobAuthTypeHash.Bytes(),
		common.LeftPadBytes(address.Bytes(), 32),
		crypto.Keccak256([]byte(timestamp)),
		common.LeftPadBytes(big.NewInt(int64(nonce)).Bytes(), 32),
		crypto.Keccak256([]byte(clobAuthMessage)),
	)

	digest := crypto.Keccak256Hash(
		[]byte{0x19, 0x01},
		domainSep.Bytes(),
		structHash.Bytes(),
	)

	sig, err := crypto.Sign(digest.Bytes(), key)
	if err != nil {
		return "", fmt.Errorf("sign digest: %w", err)
	}
	// Convert V from 0/1 to 27/28 for EIP-712 compatibility.
	sig[64] += 27

	return fmt.Sprintf("0x%x", sig), nil
}

// signOrder signs an Order via EIP-712 for the Exchange contract.
func signOrder(key *ecdsa.PrivateKey, order Order, chainID int, negRisk bool) (string, error) {
	exchange := ExchangeAddress
	if negRisk {
		exchange = NegRiskExchangeAddress
	}
	domainSep := buildOrderDomainSeparator(chainID, exchange)

	salt, ok := new(big.Int).SetString(string(order.Salt), 10)
	if !ok {
		return "", fmt.Errorf("invalid salt: %s", order.Salt)
	}
	tokenID, ok := new(big.Int).SetString(order.TokenID, 10)
	if !ok {
		return "", fmt.Errorf("invalid tokenId: %s", order.TokenID)
	}
	makerAmount, ok := new(big.Int).SetString(order.MakerAmount, 10)
	if !ok {
		return "", fmt.Errorf("invalid makerAmount: %s", order.MakerAmount)
	}
	takerAmount, ok := new(big.Int).SetString(order.TakerAmount, 10)
	if !ok {
		return "", fmt.Errorf("invalid takerAmount: %s", order.TakerAmount)
	}
	expiration, ok := new(big.Int).SetString(order.Expiration, 10)
	if !ok {
		return "", fmt.Errorf("invalid expiration: %s", order.Expiration)
	}
	nonce, ok := new(big.Int).SetString(order.Nonce, 10)
	if !ok {
		return "", fmt.Errorf("invalid nonce: %s", order.Nonce)
	}
	feeRateBps, ok := new(big.Int).SetString(order.FeeRateBps, 10)
	if !ok {
		return "", fmt.Errorf("invalid feeRateBps: %s", order.FeeRateBps)
	}

	// side: BUY=0, SELL=1
	sideVal := big.NewInt(0)
	if order.Side == SideSell {
		sideVal = big.NewInt(1)
	}

	structHash := crypto.Keccak256Hash(
		orderTypeHash.Bytes(),
		common.LeftPadBytes(salt.Bytes(), 32),
		common.LeftPadBytes(common.HexToAddress(order.Maker).Bytes(), 32),
		common.LeftPadBytes(common.HexToAddress(order.Signer).Bytes(), 32),
		common.LeftPadBytes(common.HexToAddress(order.Taker).Bytes(), 32),
		common.LeftPadBytes(tokenID.Bytes(), 32),
		common.LeftPadBytes(makerAmount.Bytes(), 32),
		common.LeftPadBytes(takerAmount.Bytes(), 32),
		common.LeftPadBytes(expiration.Bytes(), 32),
		common.LeftPadBytes(nonce.Bytes(), 32),
		common.LeftPadBytes(feeRateBps.Bytes(), 32),
		common.LeftPadBytes(sideVal.Bytes(), 32),
		common.LeftPadBytes(big.NewInt(int64(order.SignatureType)).Bytes(), 32),
	)

	digest := crypto.Keccak256Hash(
		[]byte{0x19, 0x01},
		domainSep.Bytes(),
		structHash.Bytes(),
	)

	sig, err := crypto.Sign(digest.Bytes(), key)
	if err != nil {
		return "", fmt.Errorf("sign order: %w", err)
	}
	sig[64] += 27

	return fmt.Sprintf("0x%x", sig), nil
}
