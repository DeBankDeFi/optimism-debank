package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
)

// Account represents an account in the state.
type Account struct {
	Balance   string         `json:"balance"`
	Nonce     uint64         `json:"nonce"`
	Root      hexutil.Bytes  `json:"root"`
	CodeHash  hexutil.Bytes  `json:"codeHash"`
	Code      hexutil.Bytes  `json:"code,omitempty"`
	Address   common.Address `json:"address,omitempty"`
	SecureKey hexutil.Bytes  `json:"key,omitempty"`
}

// emptyCodeHash is the known hash of an account with no code.
var emptyCodeHash = crypto.Keccak256(nil)

func main() {
	// Open an existing Ethereum database
	db, err := rawdb.NewLevelDBDatabase(os.Args[1], 16, 16, "", true)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	stateDB := state.NewDatabase(db)

	// Retrieve the head block
	hash := rawdb.ReadHeadBlockHash(db)
	number := rawdb.ReadHeaderNumber(db, hash)
	if number == nil {
		log.Fatalf("Failed to retrieve head block number")
	}
	head := rawdb.ReadBlock(db, hash, *number)
	if head == nil {
		log.Fatalf("Failed to retrieve head block")
	}

	// Retrieve the state belonging to the head block
	st, err := trie.New(trie.StateTrieID(head.Root()), trie.NewDatabase(db))
	if err != nil {
		log.Fatalf("Failed to retrieve account trie: %v", err)
	}
	log.Printf("Indexing state trie at head block #%d [0x%x]", *number, hash)

	// Iterate over the entire account trie to search for EOF-prefixed contracts
	start := time.Now()
	missingPreimages := uint64(0)
	eoas := uint64(0)
	nonEofContracts := uint64(0)
	eofContracts := make([]Account, 0)

	it := trie.NewIterator(st.NodeIterator(nil))
	for it.Next() {
		// Decode the state account
		var data types.StateAccount
		rlp.DecodeBytes(it.Value, &data)

		// Check to see if the account has any code associated with it before performing
		// more reads from the trie & db.
		if bytes.Equal(data.CodeHash, emptyCodeHash) {
			eoas++
			continue
		}

		// Create a serializable `Account` object
		account := Account{
			Balance:   data.Balance.String(),
			Nonce:     data.Nonce,
			Root:      data.Root[:],
			CodeHash:  data.CodeHash,
			SecureKey: it.Key,
		}

		// Attempt to get the address of the account from the trie
		addrBytes := st.Get(it.Key)
		if addrBytes == nil {
			// Preimage missing! Cannot continue.
			missingPreimages++
			continue
		}
		addr := common.BytesToAddress(addrBytes)

		// Attempt to get the code of the account from the trie
		code, err := stateDB.ContractCode(crypto.Keccak256Hash(addrBytes), common.BytesToHash(data.CodeHash))
		if err != nil {
			log.Fatalf("Could not load code for account %x: %v", addr, err)
			continue
		}

		// Check if the contract's runtime bytecode starts with the EOF prefix.
		if len(code) >= 1 && code[0] == 0xEF {
			// Append the account to the list of EOF contracts
			account.Address = addr
			account.Code = code
			eofContracts = append(eofContracts, account)
		} else {
			nonEofContracts++
		}
	}

	// Print finishing status
	log.Printf("Indexing done in %v, found %d EOF contracts", time.Since(start), len(eofContracts))
	log.Printf("Num missing preimages: %d", missingPreimages)
	log.Printf("Non-EOF-prefixed contracts: %d", nonEofContracts)
	log.Printf("Accounts with no code (EOAs): %d", eoas)

	// Write the EOF contracts to a file
	file, err := json.MarshalIndent(eofContracts, "", " ")
	if err != nil {
		log.Fatalf("Cannot marshal EOF contracts: %v", err)
	}
	err = ioutil.WriteFile("eof_contracts.json", file, 0644)

	log.Printf("Wrote list of EOF contracts to `eof_contracts.json`")
}
