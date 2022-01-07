package lib

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/dgraph-io/ristretto/z"
	"io"
	"log"
	"math"
	"math/big"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/btcsuite/btcd/btcec"
	"github.com/davecgh/go-spew/spew"
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/glog"
	"github.com/pkg/errors"
)

// This file contains all of the functions that interact with the database.

const (
	// BadgerDbFolder is the subfolder in the config dir where we
	// store the badgerdb database by default.
	BadgerDbFolder = "badgerdb"
)

var (
	// The key prefixes for the key-value database. To store a particular
	// type of data, we create a key prefix and store all those types of
	// data with a key prefixed by that key prefix.
	// Bitcoin does a similar thing that you can see at this link:
	// https://bitcoin.stackexchange.com/questions/28168/what-are-the-keys-used-in-the-blockchain-leveldb-ie-what-are-the-keyvalue-pair

	// The prefix for the block index:
	// Key format: <hash BlockHash>
	// Value format: serialized MsgDeSoBlock
	_PrefixBlockHashToBlock = []byte{0}

	// The prefix for the node index that we use to reconstruct the block tree.
	// Storing the height in big-endian byte order allows us to read in all the
	// blocks in height-sorted order from the db and construct the block tree by connecting
	// nodes to their parents as we go.
	//
	// Key format: <height uint32 (big-endian), hash BlockHash>
	// Value format: serialized BlockNode
	_PrefixHeightHashToNodeInfo        = []byte{1}
	_PrefixBitcoinHeightHashToNodeInfo = []byte{2}

	// We store the hash of the node that is the current tip of the main chain.
	// This key is used to look it up.
	// Value format: BlockHash
	_KeyBestDeSoBlockHash = []byte{3}

	_KeyBestBitcoinHeaderHash = []byte{4}

	// Utxo table.
	// <txid BlockHash, output_index uint64> -> UtxoEntry
	_PrefixUtxoKeyToUtxoEntry = []byte{5}
	// <prefix, pubKey [33]byte, utxoKey< txid BlockHash, index uint32 >> -> <>
	_PrefixPubKeyUtxoKey = []byte{7}
	// The number of utxo entries in the database.
	_KeyUtxoNumEntries = []byte{8}
	// Utxo operations table.
	// This table contains, for each blockhash on the main chain, the UtxoOperations
	// that were applied by this block. To roll back the block, one must loop through
	// the UtxoOperations for a particular block backwards and invert them.
	//
	// < hash *BlockHash > -> < serialized []UtxoOperation using gob encoding >
	_PrefixBlockHashToUtxoOperations = []byte{9}

	// The below are mappings related to the validation of BitcoinExchange transactions.
	//
	// The number of nanos that has been purchased thus far.
	_KeyNanosPurchased = []byte{10}
	// How much Bitcoin is work in USD cents.
	_KeyUSDCentsPerBitcoinExchangeRate = []byte{27}
	// <key> -> <GlobalParamsEntry gob serialized>
	_KeyGlobalParams = []byte{40}

	// The prefix for the Bitcoin TxID map. If a key is set for a TxID that means this
	// particular TxID has been processed as part of a BitcoinExchange transaction. If
	// no key is set for a TxID that means it has not been processed (and thus it can be
	// used to create new nanos).
	// <BitcoinTxID BlockHash> -> <nothing>
	_PrefixBitcoinBurnTxIDs = []byte{11}

	// Messages are indexed by the public key of their senders and receivers. If
	// a message sends from pkFrom to pkTo then there will be two separate entries,
	// one for pkFrom and one for pkTo. The exact format is as follows:
	// <public key (33 bytes) || uint64 big-endian> -> < SenderPublicKey || RecipientPublicKey || EncryptedText >
	_PrefixPublicKeyTimestampToPrivateMessage = []byte{12}

	// Tracks the tip of the transaction index. This is used to determine
	// which blocks need to be processed in order to update the index.
	_KeyTransactionIndexTip = []byte{14}
	// <prefix, transactionID BlockHash> -> <TransactionMetadata struct>
	_PrefixTransactionIDToMetadata = []byte{15}
	// <prefix, publicKey []byte, index uint32> -> <txid BlockHash>
	_PrefixPublicKeyIndexToTransactionIDs = []byte{16}
	// <prefx, publicKey []byte> -> <index uint32>
	_PrefixPublicKeyToNextIndex = []byte{42}

	// Main post index.
	// <prefix, PostHash BlockHash> -> PostEntry
	_PrefixPostHashToPostEntry = []byte{17}

	// Post sorts
	// <prefix, publicKey [33]byte, PostHash> -> <>
	_PrefixPosterPublicKeyPostHash = []byte{18}

	// <prefix, tstampNanos uint64, PostHash> -> <>
	_PrefixTstampNanosPostHash = []byte{19}
	// <prefix, creatorbps uint64, PostHash> -> <>
	_PrefixCreatorBpsPostHash = []byte{20}
	// <prefix, multiplebps uint64, PostHash> -> <>
	_PrefixMultipleBpsPostHash = []byte{21}

	// Comments are just posts that have their ParentStakeID set, and
	// so we have a separate index that allows us to return all the
	// comments for a given StakeID
	// <prefix, parent stakeID [33]byte, tstampnanos uint64, post hash> -> <>
	_PrefixCommentParentStakeIDToPostHash = []byte{22}

	// Main profile index
	// <prefix, PKID [33]byte> -> ProfileEntry
	_PrefixPKIDToProfileEntry = []byte{23}

	// Profile sorts
	// For username, we set the PKID as a value since the username is not fixed width.
	// We always lowercase usernames when using them as map keys in order to make
	// all uniqueness checks case-insensitive
	// <prefix, username> -> <PKID>
	_PrefixProfileUsernameToPKID = []byte{25}
	// This allows us to sort the profiles by the value of their coin (since
	// the amount of DeSo locked in a profile is proportional to coin price).
	_PrefixCreatorDeSoLockedNanosCreatorPKID = []byte{32}

	// The StakeID is a post hash for posts and a public key for users.
	// <StakeIDType | AmountNanos uint64 | StakeID [var]byte> -> <>
	_PrefixStakeIDTypeAmountStakeIDIndex = []byte{26}

	// Prefixes for follows:
	// <prefix, follower PKID [33]byte, followed PKID [33]byte> -> <>
	// <prefix, followed PKID [33]byte, follower PKID [33]byte> -> <>
	_PrefixFollowerPKIDToFollowedPKID = []byte{28}
	_PrefixFollowedPKIDToFollowerPKID = []byte{29}

	// Prefixes for likes:
	// <prefix, user pub key [33]byte, liked post hash [32]byte> -> <>
	// <prefix, post hash [32]byte, user pub key [33]byte> -> <>
	_PrefixLikerPubKeyToLikedPostHash = []byte{30}
	_PrefixLikedPostHashToLikerPubKey = []byte{31}

	// Prefixes for creator coin fields:
	// <prefix, HODLer PKID [33]byte, creator PKID [33]byte> -> <BalanceEntry>
	// <prefix, creator PKID [33]byte, HODLer PKID [33]byte> -> <BalanceEntry>
	_PrefixHODLerPKIDCreatorPKIDToBalanceEntry = []byte{33}
	_PrefixCreatorPKIDHODLerPKIDToBalanceEntry = []byte{34}

	_PrefixPosterPublicKeyTimestampPostHash = []byte{35}

	// If no mapping exists for a particular public key, then the PKID is simply
	// the public key itself.
	// <[33]byte> -> <PKID [33]byte>
	_PrefixPublicKeyToPKID = []byte{36}
	// <PKID [33]byte> -> <PublicKey [33]byte>
	_PrefixPKIDToPublicKey = []byte{37}

	// Prefix for storing mempool transactions in badger. These stored transactions are
	// used to restore the state of a node after it is shutdown.
	// <prefix, tx hash BlockHash> -> <*MsgDeSoTxn>
	_PrefixMempoolTxnHashToMsgDeSoTxn = []byte{38}

	// Prefixes for Reposts:
	// <prefix, user pub key [39]byte, reposted post hash [39]byte> -> RepostEntry
	_PrefixReposterPubKeyRepostedPostHashToRepostPostHash = []byte{39}

	// Prefixes for diamonds:
	//  <prefix, DiamondReceiverPKID [33]byte, DiamondSenderPKID [33]byte, posthash> -> <gob-encoded DiamondEntry>
	//  <prefix, DiamondSenderPKID [33]byte, DiamondReceiverPKID [33]byte, posthash> -> <gob-encoded DiamondEntry>
	_PrefixDiamondReceiverPKIDDiamondSenderPKIDPostHash = []byte{41}
	_PrefixDiamondSenderPKIDDiamondReceiverPKIDPostHash = []byte{43}

	// Public keys that have been restricted from signing blocks.
	// <prefix, ForbiddenPublicKey [33]byte> -> <>
	_PrefixForbiddenBlockSignaturePubKeys = []byte{44}

	// These indexes are used in order to fetch the pub keys of users that liked or diamonded a post.
	// 		Reposts: <prefix, RepostedPostHash, ReposterPubKey> -> <>
	// 		Quote Reposts: <prefix, RepostedPostHash, ReposterPubKey, RepostPostHash> -> <>
	// 		Diamonds: <prefix, DiamondedPostHash, DiamonderPubKey [33]byte> -> <DiamondLevel (uint64)>
	_PrefixRepostedPostHashReposterPubKey               = []byte{45}
	_PrefixRepostedPostHashReposterPubKeyRepostPostHash = []byte{46}
	_PrefixDiamondedPostHashDiamonderPKIDDiamondLevel   = []byte{47}

	// Prefixes for NFT ownership:
	// 	<prefix, NFTPostHash [32]byte, SerialNumber uint64> -> NFTEntry
	_PrefixPostHashSerialNumberToNFTEntry = []byte{48}
	//  <prefix, PKID [33]byte, IsForSale bool, BidAmountNanos uint64, NFTPostHash[32]byte, SerialNumber uint64> -> NFTEntry
	_PrefixPKIDIsForSaleBidAmountNanosPostHashSerialNumberToNFTEntry = []byte{49}

	// Prefixes for NFT bids:
	//  <prefix, NFTPostHash [32]byte, SerialNumber uint64, BidNanos uint64, PKID [33]byte> -> <>
	_PrefixPostHashSerialNumberBidNanosBidderPKID = []byte{50}
	//  <BidderPKID [33]byte, NFTPostHash [32]byte, SerialNumber uint64> -> <BidNanos uint64>
	_PrefixBidderPKIDPostHashSerialNumberToBidNanos = []byte{51}

	// Prefix for NFT accepted bid entries:
	//   - Note: this index uses a slice to track the history of winning bids for an NFT. It is
	//     not core to consensus and should not be relied upon as it could get inefficient.
	//   - Schema: <prefix>, NFTPostHash [32]byte, SerialNumber uint64 -> []NFTBidEntry
	_PrefixPostHashSerialNumberToAcceptedBidEntries = []byte{54}

	// <prefix, PublicKey [33]byte> -> uint64
	_PrefixPublicKeyToDeSoBalanceNanos = []byte{52}
	// Block reward prefix:
	//   - This index is needed because block rewards take N blocks to mature, which means we need
	//     a way to deduct them from balance calculations until that point. Without this index, it
	//     would be impossible to figure out which of a user's UTXOs have yet to mature.
	//   - Schema: <hash BlockHash> -> <pubKey [33]byte, uint64 blockRewardNanos>
	_PrefixPublicKeyBlockHashToBlockReward = []byte{53}

	// Prefix for Authorize Derived Key transactions:
	// 		<prefix, OwnerPublicKey [33]byte> -> <>
	_PrefixAuthorizeDerivedKey = []byte{54}

	// TODO: This process is a bit error-prone. We should come up with a test or
	// something to at least catch cases where people have two prefixes with the
	// same ID.
	// NEXT_TAG: 55
	_PrefixAncestralRecords = []byte{57}
)

var StatePrefixes = [][]byte{
	_PrefixUtxoKeyToUtxoEntry,
	_PrefixPubKeyUtxoKey,
	_KeyUtxoNumEntries,
	_KeyNanosPurchased,
	_KeyGlobalParams,
	_PrefixBitcoinBurnTxIDs,
	_PrefixPublicKeyTimestampToPrivateMessage,
	_PrefixPostHashToPostEntry,
	_PrefixPosterPublicKeyPostHash,
	_PrefixTstampNanosPostHash,
	_PrefixCreatorBpsPostHash,
	_PrefixMultipleBpsPostHash,
	_PrefixCommentParentStakeIDToPostHash,
	_PrefixPKIDToProfileEntry,
	_PrefixProfileUsernameToPKID,
	_PrefixCreatorDeSoLockedNanosCreatorPKID,
	_PrefixStakeIDTypeAmountStakeIDIndex,
	_PrefixFollowerPKIDToFollowedPKID,
	_PrefixFollowedPKIDToFollowerPKID,
	_PrefixLikerPubKeyToLikedPostHash,
	_PrefixLikedPostHashToLikerPubKey,
	_PrefixHODLerPKIDCreatorPKIDToBalanceEntry,
	_PrefixCreatorPKIDHODLerPKIDToBalanceEntry,
	_PrefixPosterPublicKeyTimestampPostHash,
	_PrefixPublicKeyToPKID,
	_PrefixPKIDToPublicKey,
	_PrefixReposterPubKeyRepostedPostHashToRepostPostHash,
	_PrefixDiamondReceiverPKIDDiamondSenderPKIDPostHash,
	_PrefixDiamondSenderPKIDDiamondReceiverPKIDPostHash,
	_PrefixForbiddenBlockSignaturePubKeys,
	_PrefixRepostedPostHashReposterPubKey,
	_PrefixRepostedPostHashReposterPubKeyRepostPostHash,
	_PrefixDiamondedPostHashDiamonderPKIDDiamondLevel,
	_PrefixPostHashSerialNumberToNFTEntry,
	_PrefixPKIDIsForSaleBidAmountNanosPostHashSerialNumberToNFTEntry,
	_PrefixPostHashSerialNumberBidNanosBidderPKID,
	_PrefixBidderPKIDPostHashSerialNumberToBidNanos,
	_PrefixPostHashSerialNumberToAcceptedBidEntries,
	_PrefixPublicKeyToDeSoBalanceNanos,
	_PrefixPublicKeyBlockHashToBlockReward,
	_PrefixAuthorizeDerivedKey,
}

var NonStatePrefixes = [][]byte{
	_PrefixBlockHashToBlock,
	_PrefixHeightHashToNodeInfo,
	_PrefixBitcoinHeightHashToNodeInfo,
	_KeyBestDeSoBlockHash,
	_KeyBestBitcoinHeaderHash,
	_PrefixBlockHashToUtxoOperations,
	_PrefixTransactionIDToMetadata,
	_PrefixPublicKeyIndexToTransactionIDs,
	_KeyUSDCentsPerBitcoinExchangeRate,
	_KeyTransactionIndexTip,
	_PrefixPublicKeyToNextIndex,
	_PrefixMempoolTxnHashToMsgDeSoTxn,
	_PrefixAncestralRecords,
}

func isStateKey(key []byte) bool {
	isStatePrefix := true
	for ii := 0; ii < len(NonStatePrefixes); ii++ {
		if reflect.DeepEqual(NonStatePrefixes[ii][0], key[0]) {
			isStatePrefix = false
			break
		}
	}
	return isStatePrefix
}

func EncodeKeyValue(key []byte, value []byte) []byte {
	data := []byte{}

	// Encode key and value similarly to how DER signatures are encoded
	// len(key + value) || len(key) || key || len(value) || value
	// This ensures integrity of the (key, value) pairs
	data = append(data, UintToBuf(uint64(len(key)+len(value)))...)
	data = append(data, UintToBuf(uint64(len(key)))...)
	data = append(data, key...)
	data = append(data, UintToBuf(uint64(len(value)))...)
	data = append(data, value...)

	return data
}

// DBSetWithTxn is a wrapper around BadgerDB Set function which allows us to add
// computation prior to DB writes. In particular, we use it to maintain a dynamic
// LRU cache, and to build DB snapshots with ancestral records.
func DBSetWithTxn(txn *badger.Txn, snap *Snapshot, key []byte, value []byte) error {
	// We only cache / update ancestral records when we're dealing with state prefix.
	isState := snap != nil && snap.isState(key)
	var ancestralValue []byte
	var getError error

	// If snapshot was provided, we will need to load the current value of the record
	// so that we can later write it in the ancestral record. We first lookup cache.
	if isState {
		// We check if we've already read this key and stored it in the cache.
		// Otherwise, we fetch the current value of this record from the DB.
		ancestralValue, getError = DBGetWithTxn(txn, snap, key)

		// If there is some error with the DB read, other than non-existent key, we return.
		if getError != nil && getError != badger.ErrKeyNotFound {
			return errors.Wrapf(getError, "DBSetWithTxn: problem reading record " +
				"from DB with key: %v", key)
		}
	}

	// We update the DB record with the intended value.
	err := txn.Set(key, value)
	if err != nil {
		return errors.Wrapf(err, "DBSetWithTxn: Problem setting record " +
			"in DB with key: %v, value: %v", key, value)
	}

	// After a successful DB write, we update the snapshot.
	if isState {
		keyString := hex.EncodeToString(key)
		// Update ancestral record structures depending on the existing DB record.
		snap.PrepareAncestralRecord(keyString, ancestralValue, getError != badger.ErrKeyNotFound)
		// Now save the newest record to cache.
		snap.Cache.Add(keyString, value)

		// We have to remove the previous value from the state checksum.
		// Because checksum is commutative, we can safely remove the past value here.
		snap.Checksum.RemoveBytes(EncodeKeyValue(key, ancestralValue))
		// We also add the new record to the checksum.
		snap.Checksum.AddBytes(EncodeKeyValue(key, value))
	}
	return nil
}

// DBGetWithTxn is a wrapper function around the BadgerDB get function. It returns
// the DB entry associated with the given key and handles the logic around the LRU cache.
// Whenever we read/write records in the DB, we place a copy in the LRU cache to save
// us lookup time.
func DBGetWithTxn(txn *badger.Txn, snap *Snapshot, key []byte) ([]byte, error) {
	// We only cache / update ancestral records when we're dealing with state prefix.
	isState := snap != nil && snap.isState(key)
	keyString := hex.EncodeToString(key)

	// Lookup the snapshot cache and check if we've already stored a value there.
	if isState {
		if val, exists := snap.Cache.Lookup(keyString); exists {
			return val.([]byte), nil
		}
	}

	// If record doesn't exist in cache, we get it from the DB.
	item, err := txn.Get(key)
	if err != nil {
		return nil, err
	}
	itemData, err := item.ValueCopy(nil)

	// If DBWriteSemaphore semaphore indicates that a flush takes place, we don't update cache.
	if isState && atomic.LoadInt32(&snap.DBWriteSemaphore) == 0 {
		snap.Cache.Add(keyString, itemData)
	}
	return itemData, nil
}

// DBDeleteWithTxn is a wrapper function around BadgerDB delete function.
// It allows us to update the snapshot LRU cache and ancestral records.
func DBDeleteWithTxn(txn *badger.Txn, snap *Snapshot, key []byte) error {
	var ancestralValue []byte
	var getError error
	isState := snap != nil && snap.isState(key)

	// If snapshot was provided, we will need to load the current value of the record
	// so that we can later write it in the ancestral record. We first lookup cache.
	if isState {
		// We check if we've already read this key and stored it in the cache.
		// Otherwise, we fetch the current value of this record from the DB.
		ancestralValue, getError = DBGetWithTxn(txn, snap, key)

		// If there is some error with the DB read, other than non-existent key, we return.
		if getError != nil && getError != badger.ErrKeyNotFound {
			return errors.Wrapf(getError, "DBDeleteWithTxn: problem checking for DB record " +
				"with key: %v", key)
		}
	}

	err := txn.Delete(key)
	if err != nil {
		return errors.Wrapf(err, "DBDeleteWithTxn: Problem deleting record " +
			"from DB with key: %v", key)
	}

	// After a successful DB delete, we update the snapshot.
	if isState {
		keyString := hex.EncodeToString(key)
		// Update ancestral record structures depending on the existing DB record.
		snap.PrepareAncestralRecord(keyString, ancestralValue, getError != badger.ErrKeyNotFound)
		// Now delete the past record from the cache.
		snap.Cache.Delete(keyString)
		// We have to remove the previous value from the state checksum.
		// Because checksum is commutative, we can safely remove the past value here.
		snap.Checksum.RemoveBytes(EncodeKeyValue(key, ancestralValue))
	}
	return nil
}

func DBIteratePrefixKeys(db *badger.DB, prefix []byte, lastKey []byte, maxBytes uint32) (
	*[]string, *[]string, bool, error) {
	var keys, values []string
	currentBytes := 0
	full := false

	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(lastKey); it.ValidForPrefix(prefix) && !full; it.Next() {
			item := it.Item()
			k := item.Key()
			err := item.Value(func(v []byte) error {
				keys = append(keys, hex.EncodeToString(k))
				values = append(values, hex.EncodeToString(v))
				currentBytes += len(k) + len(v)
				if currentBytes > int(maxBytes) {
					full = true
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, true, err
	}
	return &keys, &values, full, nil
}

func DBStreamPrefixKeys(db *badger.DB) (*map[string][]byte, error) {
	stream := db.NewStream()
	stream.NumGo = 4
	stream.Prefix = _PrefixUtxoKeyToUtxoEntry
	stream.LogPrefix = "Badger.Streaming"

	//stream.ChooseKey = func(item *badger.Item) bool {
	//	fmt.Printf("Key: %v\n", hex.EncodeToString(item.Key()))
	//	return bytes.HasPrefix(item.Key(), _PrefixUtxoKeyToUtxoEntry)
	//}
	//stream.KeyToList = nil
	output := make(map[string][]byte)
	stream.Send = func(batch *z.Buffer) error {
		list, err := badger.BufferToKVList(batch)
		if err != nil {
			return err
		}
		for _, kv := range list.Kv {
			//fmt.Printf("Key: %v\n Value %v\n", hex.EncodeToString(kv.Key), hex.EncodeToString(kv.Value))
			output[hex.EncodeToString(kv.Key)] = kv.Value
		}
		return nil
	}
	// Run the stream
	if err := stream.Orchestrate(context.Background()); err != nil {
		return nil, err
	}
	return &output, nil
}

func DBGetPKIDEntryForPublicKeyWithTxn(txn *badger.Txn, snap *Snapshot, publicKey []byte) *PKIDEntry {
	if len(publicKey) == 0 {
		return nil
	}

	prefix := append([]byte{}, _PrefixPublicKeyToPKID...)
	pkidBytes, err := DBGetWithTxn(txn, snap, append(prefix, publicKey...))

	if err != nil {
		// If we don't have a mapping from public key to PKID in the db,
		// then we use the public key itself as the PKID. Doing this makes
		// it so that the PKID is generally the *first* public key that the
		// user ever associated with a particular piece of data.
		//glog.Errorf("DBGetPKIDEntryForPublicKeyWithTxn: Problem reading "+
		//	"PKIDEntry for public key %s",
		//	PkToStringMainnet(publicKey))

		return &PKIDEntry{
			PKID:      PublicKeyToPKID(publicKey),
			PublicKey: publicKey,
		}
	}

	// If we get here then it means we actually had a PKID in the DB.
	// So return that pkid.
	pkidEntryObj := &PKIDEntry{}
	pkidEntryObj.Decode(pkidBytes)
	return pkidEntryObj
}

func DBGetPKIDEntryForPublicKey(db *badger.DB, snap *Snapshot, publicKey []byte) *PKIDEntry {
	var pkid *PKIDEntry
	db.View(func(txn *badger.Txn) error {
		pkid = DBGetPKIDEntryForPublicKeyWithTxn(txn, snap, publicKey)
		return nil
	})
	return pkid
}

func DBGetPublicKeyForPKIDWithTxn(txn *badger.Txn, snap *Snapshot, pkidd *PKID) []byte {
	prefix := append([]byte{}, _PrefixPKIDToPublicKey...)
	pkidBytes, err := DBGetWithTxn(txn, snap, append(prefix, pkidd[:]...))

	if err != nil {
		// If we don't have a mapping in the db then return the pkid itself
		// as the public key.
		return pkidd[:]
	}

	// If we get here then it means we actually had a public key mapping in the DB.
	// So return that public key.

	return pkidBytes
}

func DBGetPublicKeyForPKID(db *badger.DB, snap *Snapshot, pkidd *PKID) []byte {
	var publicKey []byte
	db.View(func(txn *badger.Txn) error {
		publicKey = DBGetPublicKeyForPKIDWithTxn(txn, snap, pkidd)
		return nil
	})
	return publicKey
}

func DBPutPKIDMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	publicKey []byte, pkidEntry *PKIDEntry, params *DeSoParams) error {

	// Set the main pub key -> pkid mapping.
	{
		prefix := append([]byte{}, _PrefixPublicKeyToPKID...)
		pubKeyToPkidKey := append(prefix, publicKey...)
		if err := DBSetWithTxn(txn, snap, pubKeyToPkidKey, pkidEntry.Encode()); err != nil {

			return errors.Wrapf(err, "DBPutPKIDMappingsWithTxn: Problem "+
				"adding mapping for pkid: %v public key: %v",
				PkToString(pkidEntry.PKID[:], params), PkToString(publicKey, params))
		}
	}

	// Set the reverse mapping: pkid -> pub key
	{
		prefix := append([]byte{}, _PrefixPKIDToPublicKey...)
		pkidToPubKey := append(prefix, pkidEntry.PKID[:]...)
		if err := DBSetWithTxn(txn, snap, pkidToPubKey, publicKey); err != nil {

			return errors.Wrapf(err, "DBPutPKIDMappingsWithTxn: Problem "+
				"adding mapping for pkid: %v public key: %v",
				PkToString(pkidEntry.PKID[:], params), PkToString(publicKey, params))
		}
	}

	return nil
}

func DBDeletePKIDMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	publicKey []byte, params *DeSoParams) error {

	// Look up the pkid for the public key.
	pkidEntry := DBGetPKIDEntryForPublicKeyWithTxn(txn, snap, publicKey)

	{
		prefix := append([]byte{}, _PrefixPublicKeyToPKID...)
		pubKeyToPkidKey := append(prefix, publicKey...)
		if err := DBDeleteWithTxn(txn, snap, pubKeyToPkidKey); err != nil {

			return errors.Wrapf(err, "DBDeletePKIDMappingsWithTxn: Problem "+
				"deleting mapping for public key: %v",
				PkToString(publicKey, params))
		}
	}

	{
		prefix := append([]byte{}, _PrefixPKIDToPublicKey...)
		pubKeyToPkidKey := append(prefix, pkidEntry.PKID[:]...)
		if err := DBDeleteWithTxn(txn, snap, pubKeyToPkidKey); err != nil {

			return errors.Wrapf(err, "DBDeletePKIDMappingsWithTxn: Problem "+
				"deleting mapping for pkid: %v",
				PkToString(pkidEntry.PKID[:], params))
		}
	}

	return nil
}

func EnumerateKeysForPrefix(db *badger.DB, dbPrefix []byte) (_keysFound [][]byte, _valsFound [][]byte) {
	return _enumerateKeysForPrefix(db, dbPrefix)
}

// A helper function to enumerate all of the values for a particular prefix.
func _enumerateKeysForPrefix(db *badger.DB, dbPrefix []byte) (_keysFound [][]byte, _valsFound [][]byte) {
	keysFound := [][]byte{}
	valsFound := [][]byte{}

	dbErr := db.View(func(txn *badger.Txn) error {
		var err error
		keysFound, valsFound, err = _enumerateKeysForPrefixWithTxn(txn, dbPrefix)
		if err != nil {
			return err
		}
		return nil
	})
	if dbErr != nil {
		glog.Errorf("_enumerateKeysForPrefix: Problem fetching keys and values from db: %v", dbErr)
		return nil, nil
	}

	return keysFound, valsFound
}

func _enumerateKeysForPrefixWithTxn(txn *badger.Txn, dbPrefix []byte) (_keysFound [][]byte, _valsFound [][]byte, _err error) {
	keysFound := [][]byte{}
	valsFound := [][]byte{}

	opts := badger.DefaultIteratorOptions
	nodeIterator := txn.NewIterator(opts)
	defer nodeIterator.Close()
	prefix := dbPrefix
	for nodeIterator.Seek(prefix); nodeIterator.ValidForPrefix(prefix); nodeIterator.Next() {
		key := nodeIterator.Item().Key()
		keyCopy := make([]byte, len(key))
		copy(keyCopy[:], key[:])

		valCopy, err := nodeIterator.Item().ValueCopy(nil)
		if err != nil {
			return nil, nil, err
		}
		keysFound = append(keysFound, keyCopy)
		valsFound = append(valsFound, valCopy)
	}
	return keysFound, valsFound, nil
}

// A helper function to enumerate a limited number of the values for a particular prefix.
func _enumerateLimitedKeysReversedForPrefix(db *badger.DB, dbPrefix []byte, limit uint64) (_keysFound [][]byte, _valsFound [][]byte) {
	keysFound := [][]byte{}
	valsFound := [][]byte{}

	dbErr := db.View(func(txn *badger.Txn) error {
		var err error
		keysFound, valsFound, err = _enumerateLimitedKeysReversedForPrefixWithTxn(txn, dbPrefix, limit)
		return err
	})
	if dbErr != nil {
		glog.Errorf("_enumerateKeysForPrefix: Problem fetching keys and values from db: %v", dbErr)
		return nil, nil
	}

	return keysFound, valsFound
}

func _enumerateLimitedKeysReversedForPrefixWithTxn(txn *badger.Txn, dbPrefix []byte, limit uint64) (_keysFound [][]byte, _valsFound [][]byte, _err error) {
	keysFound := [][]byte{}
	valsFound := [][]byte{}

	opts := badger.DefaultIteratorOptions

	// Go in reverse order
	opts.Reverse = true

	nodeIterator := txn.NewIterator(opts)
	defer nodeIterator.Close()
	prefix := dbPrefix

	counter := uint64(0)
	for nodeIterator.Seek(append(prefix, 0xff)); nodeIterator.ValidForPrefix(prefix); nodeIterator.Next() {
		if counter == limit {
			break
		}
		counter++

		key := nodeIterator.Item().Key()
		keyCopy := make([]byte, len(key))
		copy(keyCopy[:], key[:])

		valCopy, err := nodeIterator.Item().ValueCopy(nil)
		if err != nil {
			return nil, nil, err
		}
		keysFound = append(keysFound, keyCopy)
		valsFound = append(valsFound, valCopy)
	}
	return keysFound, valsFound, nil
}

// -------------------------------------------------------------------------------------
// DeSo balance mapping functions
// -------------------------------------------------------------------------------------

func _dbKeyForPublicKeyToDeSoBalanceNanos(publicKey []byte) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixPublicKeyToDeSoBalanceNanos...)
	key := append(prefixCopy, publicKey...)
	return key
}

func DbGetPrefixForPublicKeyToDesoBalanceNanos() []byte {
	return append([]byte{}, _PrefixPublicKeyToDeSoBalanceNanos...)
}

func DbGetDeSoBalanceNanosForPublicKeyWithTxn(txn *badger.Txn, snap *Snapshot, publicKey []byte,
) (_balance uint64, _err error) {

	key := _dbKeyForPublicKeyToDeSoBalanceNanos(publicKey)

	desoBalanceBytes, err := DBGetWithTxn(txn, snap, key)
	// If balance hasn't been set before, then we would error with key not found.
	if err == badger.ErrKeyNotFound {
		return uint64(0), nil
	}
	if err != nil {
		return uint64(0), errors.Wrapf(
			err, "DbGetDeSoBalanceNanosForPublicKeyWithTxn: Problem getting balance for: %s ",
			PkToStringBoth(publicKey))
	}

	desoBalance := DecodeUint64(desoBalanceBytes)

	return desoBalance, nil
}

func DbGetDeSoBalanceNanosForPublicKey(db *badger.DB, snap *Snapshot, publicKey []byte,
) (_balance uint64, _err error) {
	ret := uint64(0)
	dbErr := db.View(func(txn *badger.Txn) error {
		var err error
		ret, err = DbGetDeSoBalanceNanosForPublicKeyWithTxn(txn, snap, publicKey)
		if err != nil {
			return fmt.Errorf("DbGetDeSoBalanceNanosForPublicKey: %v", err)
		}
		return nil
	})
	if dbErr != nil {
		return ret, dbErr
	}
	return ret, nil
}

func DbPutDeSoBalanceForPublicKeyWithTxn(txn *badger.Txn, snap *Snapshot,
	publicKey []byte, balanceNanos uint64) error {

	if len(publicKey) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutDeSoBalanceForPublicKeyWithTxn: Public key "+
			"length %d != %d", len(publicKey), btcec.PubKeyBytesLenCompressed)
	}

	balanceBytes := EncodeUint64(balanceNanos)

	if err := DBSetWithTxn(txn, snap, _dbKeyForPublicKeyToDeSoBalanceNanos(publicKey), balanceBytes); err != nil {

		return errors.Wrapf(
			err, "DbPutDeSoBalanceForPublicKey: Problem adding balance mapping of %d for: %s ",
			balanceNanos, PkToStringBoth(publicKey))
	}

	return nil
}

func DbPutDeSoBalanceForPublicKey(handle *badger.DB, snap *Snapshot,
	publicKey []byte, balanceNanos uint64) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutDeSoBalanceForPublicKeyWithTxn(txn, snap, publicKey, balanceNanos)
	})
}

func DbDeletePublicKeyToDeSoBalanceWithTxn(txn *badger.Txn, snap *Snapshot, publicKey []byte) error {

	if err := DBDeleteWithTxn(txn, snap, _dbKeyForPublicKeyToDeSoBalanceNanos(publicKey)); err != nil {
		return errors.Wrapf(err, "DbDeletePublicKeyToDeSoBalanceWithTxn: Problem deleting "+
			"balance for public key %s", PkToStringMainnet(publicKey))
	}

	return nil
}

func DbDeletePublicKeyToDeSoBalance(handle *badger.DB, snap *Snapshot, publicKey []byte) error {
	return handle.Update(func(txn *badger.Txn) error {
		return DbDeletePublicKeyToDeSoBalanceWithTxn(txn, snap, publicKey)
	})
}

// -------------------------------------------------------------------------------------
// PrivateMessage mapping functions
// <public key (33 bytes) || uint64 big-endian> ->
// 		< SenderPublicKey || RecipientPublicKey || EncryptedText >
// -------------------------------------------------------------------------------------

func _dbKeyForMessageEntry(publicKey []byte, tstampNanos uint64) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixPublicKeyTimestampToPrivateMessage...)
	key := append(prefixCopy, publicKey...)
	key = append(key, EncodeUint64(tstampNanos)...)
	return key
}

func _dbSeekPrefixForMessagePublicKey(publicKey []byte) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixPublicKeyTimestampToPrivateMessage...)
	return append(prefixCopy, publicKey...)
}

// Note that this adds a mapping for the sender *and* the recipient.
func DbPutMessageEntryWithTxn(txn *badger.Txn, snap *Snapshot,
	messageEntry *MessageEntry) error {

	if len(messageEntry.SenderPublicKey) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutPrivateMessageWithTxn: Sender public key "+
			"length %d != %d", len(messageEntry.SenderPublicKey), btcec.PubKeyBytesLenCompressed)
	}
	if len(messageEntry.RecipientPublicKey) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutPrivateMessageWithTxn: Recipient public key "+
			"length %d != %d", len(messageEntry.RecipientPublicKey), btcec.PubKeyBytesLenCompressed)
	}

	messageEntryBytes := messageEntry.Encode()

	if err := DBSetWithTxn(txn, snap, _dbKeyForMessageEntry(
		messageEntry.SenderPublicKey, messageEntry.TstampNanos), messageEntryBytes); err != nil {

		return errors.Wrapf(err, "DbPutMessageEntryWithTxn: Problem adding mapping for sender: ")
	}
	if err := DBSetWithTxn(txn, snap, _dbKeyForMessageEntry(
		messageEntry.RecipientPublicKey, messageEntry.TstampNanos), messageEntryBytes); err != nil {

		return errors.Wrapf(err, "DbPutMessageEntryWithTxn: Problem adding mapping for recipient: ")
	}

	return nil
}

func DbPutMessageEntry(handle *badger.DB, snap *Snapshot, messageEntry *MessageEntry) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutMessageEntryWithTxn(txn, snap, messageEntry)
	})
}

func DbGetMessageEntryWithTxn(txn *badger.Txn, snap *Snapshot,
	publicKey []byte, tstampNanos uint64) *MessageEntry {

	key := _dbKeyForMessageEntry(publicKey, tstampNanos)
	privateMessageBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		if err != badger.ErrKeyNotFound {
			glog.Errorf("DbGetMessageEntryWithTxn: Problem reading "+
				"MessageEntry for public key %s with tstampnanos %d",
				PkToStringMainnet(publicKey), tstampNanos)
		}
		return nil
	}

	privateMessageObj := &MessageEntry{}
	privateMessageObj.Decode(privateMessageBytes)
	return privateMessageObj
}

func DbGetMessageEntry(db *badger.DB, snap *Snapshot,
	publicKey []byte, tstampNanos uint64) *MessageEntry {

	var ret *MessageEntry
	db.View(func(txn *badger.Txn) error {
		ret = DbGetMessageEntryWithTxn(txn, snap, publicKey, tstampNanos)
		return nil
	})
	return ret
}

// Note this deletes the message for the sender *and* receiver since a mapping
// should exist for each.
func DbDeleteMessageEntryMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	publicKey []byte, tstampNanos uint64) error {

	// First pull up the mapping that exists for the public key passed in.
	// If one doesn't exist then there's nothing to do.
	existingMessage := DbGetMessageEntryWithTxn(txn, snap, publicKey, tstampNanos)
	if existingMessage == nil {
		return nil
	}

	// When a message exists, delete the mapping for the sender and receiver.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForMessageEntry(existingMessage.SenderPublicKey, tstampNanos)); err != nil {
		return errors.Wrapf(err, "DbDeleteMessageEntryMappingsWithTxn: Deleting "+
			"sender mapping for public key %s and tstamp %d failed",
			PkToStringMainnet(existingMessage.SenderPublicKey), tstampNanos)
	}
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForMessageEntry(existingMessage.RecipientPublicKey, tstampNanos)); err != nil {
		return errors.Wrapf(err, "DbDeleteMessageEntryMappingsWithTxn: Deleting "+
			"recipient mapping for public key %s and tstamp %d failed",
			PkToStringMainnet(existingMessage.RecipientPublicKey), tstampNanos)
	}

	return nil
}

func DbDeleteMessageEntryMappings(handle *badger.DB, snap *Snapshot,
	publicKey []byte, tstampNanos uint64) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbDeleteMessageEntryMappingsWithTxn(txn, snap, publicKey, tstampNanos)
	})
}

func DbGetMessageEntriesForPublicKey(handle *badger.DB, publicKey []byte) (
	_privateMessages []*MessageEntry, _err error) {

	// Setting the prefix to a tstamp of zero should return all the messages
	// for the public key in sorted order since 0 << the minimum timestamp in
	// the db.
	prefix := _dbSeekPrefixForMessagePublicKey(publicKey)

	// Goes backwards to get messages in time sorted order.
	// Limit the number of keys to speed up load times.
	_, valuesFound := _enumerateKeysForPrefix(handle, prefix)

	privateMessages := []*MessageEntry{}
	for _, valBytes := range valuesFound {
		privateMessageObj := &MessageEntry{}
		privateMessageObj.Decode(valBytes)

		privateMessages = append(privateMessages, privateMessageObj)
	}

	return privateMessages, nil
}

func DbGetLimitedMessageEntriesForPublicKey(handle *badger.DB, publicKey []byte) (
	_privateMessages []*MessageEntry, _err error) {

	// Setting the prefix to a tstamp of zero should return all the messages
	// for the public key in sorted order since 0 << the minimum timestamp in
	// the db.
	prefix := _dbSeekPrefixForMessagePublicKey(publicKey)

	// Goes backwards to get messages in time sorted order.
	// Limit the number of keys to speed up load times.
	_, valuesFound := _enumerateLimitedKeysReversedForPrefix(handle, prefix, uint64(MessagesToFetchPerInboxCall))

	privateMessages := []*MessageEntry{}
	for _, valBytes := range valuesFound {
		privateMessageObj := &MessageEntry{}
		if err := gob.NewDecoder(bytes.NewReader(valBytes)).Decode(privateMessageObj); err != nil {
			return nil, errors.Wrapf(
				err, "DbGetMessageEntriesForPublicKey: Problem decoding value: ")
		}

		privateMessages = append(privateMessages, privateMessageObj)
	}

	return privateMessages, nil
}

// -------------------------------------------------------------------------------------
// Forbidden block signature public key functions
// <prefix, public key> -> <>
// -------------------------------------------------------------------------------------

func _dbKeyForForbiddenBlockSignaturePubKeys(publicKey []byte) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixForbiddenBlockSignaturePubKeys...)
	key := append(prefixCopy, publicKey...)
	return key
}

func DbPutForbiddenBlockSignaturePubKeyWithTxn(txn *badger.Txn, snap *Snapshot, publicKey []byte) error {

	if len(publicKey) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutForbiddenBlockSignaturePubKeyWithTxn: Forbidden public key "+
			"length %d != %d", len(publicKey), btcec.PubKeyBytesLenCompressed)
	}

	if err := DBSetWithTxn(txn, snap, _dbKeyForForbiddenBlockSignaturePubKeys(publicKey), []byte{}); err != nil {
		return errors.Wrapf(err, "DbPutForbiddenBlockSignaturePubKeyWithTxn: Problem adding mapping for sender: ")
	}

	return nil
}

func DbPutForbiddenBlockSignaturePubKey(handle *badger.DB, snap *Snapshot, publicKey []byte) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutForbiddenBlockSignaturePubKeyWithTxn(txn, snap, publicKey)
	})
}

func DbGetForbiddenBlockSignaturePubKeyWithTxn(txn *badger.Txn, snap *Snapshot, publicKey []byte) []byte {

	key := _dbKeyForForbiddenBlockSignaturePubKeys(publicKey)
	_, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	// Typically, we return a DB entry here, but we don't store anything for this mapping.
	// We use this function instead of one returning true / false for feature consistency.
	return []byte{}
}

func DbGetForbiddenBlockSignaturePubKey(db *badger.DB, snap *Snapshot, publicKey []byte) []byte {
	var ret []byte
	db.View(func(txn *badger.Txn) error {
		ret = DbGetForbiddenBlockSignaturePubKeyWithTxn(txn, snap, publicKey)
		return nil
	})
	return ret
}

func DbDeleteForbiddenBlockSignaturePubKeyWithTxn(
	txn *badger.Txn, snap *Snapshot, publicKey []byte) error {

	existingEntry := DbGetForbiddenBlockSignaturePubKeyWithTxn(txn, snap, publicKey)
	if existingEntry == nil {
		return nil
	}

	if err := DBDeleteWithTxn(txn, snap, _dbKeyForForbiddenBlockSignaturePubKeys(publicKey)); err != nil {
		return errors.Wrapf(err, "DbDeleteForbiddenBlockSignaturePubKeyWithTxn: Deleting "+
			"sender mapping for public key %s failed", PkToStringMainnet(publicKey))
	}

	return nil
}

func DbDeleteForbiddenBlockSignaturePubKey(
	handle *badger.DB, snap *Snapshot, publicKey []byte) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbDeleteForbiddenBlockSignaturePubKeyWithTxn(txn, snap, publicKey)
	})
}

// -------------------------------------------------------------------------------------
// Likes mapping functions
// 		<prefix, user pub key [33]byte, liked post BlockHash> -> <>
// 		<prefix, liked post BlockHash, user pub key [33]byte> -> <>
// -------------------------------------------------------------------------------------

func _dbKeyForLikerPubKeyToLikedPostHashMapping(
	userPubKey []byte, likedPostHash BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixLikerPubKeyToLikedPostHash...)
	key := append(prefixCopy, userPubKey...)
	key = append(key, likedPostHash[:]...)
	return key
}

func _dbKeyForLikedPostHashToLikerPubKeyMapping(
	likedPostHash BlockHash, userPubKey []byte) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixLikedPostHashToLikerPubKey...)
	key := append(prefixCopy, likedPostHash[:]...)
	key = append(key, userPubKey...)
	return key
}

func _dbSeekPrefixForPostHashesYouLike(yourPubKey []byte) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixLikerPubKeyToLikedPostHash...)
	return append(prefixCopy, yourPubKey...)
}

func _dbSeekPrefixForLikerPubKeysLikingAPostHash(likedPostHash BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixLikedPostHashToLikerPubKey...)
	return append(prefixCopy, likedPostHash[:]...)
}

// Note that this adds a mapping for the user *and* the liked post.
func DbPutLikeMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	userPubKey []byte, likedPostHash BlockHash) error {

	if len(userPubKey) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutLikeMappingsWithTxn: User public key "+
			"length %d != %d", len(userPubKey), btcec.PubKeyBytesLenCompressed)
	}

	if err := DBSetWithTxn(txn, snap, _dbKeyForLikerPubKeyToLikedPostHashMapping(
		userPubKey, likedPostHash), []byte{}); err != nil {

		return errors.Wrapf(
			err, "DbPutLikeMappingsWithTxn: Problem adding user to liked post mapping: ")
	}
	if err := DBSetWithTxn(txn, snap, _dbKeyForLikedPostHashToLikerPubKeyMapping(
		likedPostHash, userPubKey), []byte{}); err != nil {

		return errors.Wrapf(
			err, "DbPutLikeMappingsWithTxn: Problem adding liked post to user mapping: ")
	}

	return nil
}

func DbPutLikeMappings(handle *badger.DB, snap *Snapshot,
	userPubKey []byte, likedPostHash BlockHash) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutLikeMappingsWithTxn(txn, snap, userPubKey, likedPostHash)
	})
}

func DbGetLikerPubKeyToLikedPostHashMappingWithTxn(txn *badger.Txn,
	snap *Snapshot, userPubKey []byte, likedPostHash BlockHash) []byte {

	key := _dbKeyForLikerPubKeyToLikedPostHashMapping(userPubKey, likedPostHash)
	_, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	// Typically, we return a DB entry here, but we don't store anything for like mappings.
	// We use this function instead of one returning true / false for feature consistency.
	return []byte{}
}

func DbGetLikerPubKeyToLikedPostHashMapping(
	db *badger.DB, snap *Snapshot, userPubKey []byte, likedPostHash BlockHash) []byte {
	var ret []byte
	db.View(func(txn *badger.Txn) error {
		ret = DbGetLikerPubKeyToLikedPostHashMappingWithTxn(txn, snap, userPubKey, likedPostHash)
		return nil
	})
	return ret
}

// Note this deletes the like for the user *and* the liked post since a mapping
// should exist for each.
func DbDeleteLikeMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	userPubKey []byte, likedPostHash BlockHash) error {

	// First check that a mapping exists. If one doesn't exist then there's nothing to do.
	existingMapping := DbGetLikerPubKeyToLikedPostHashMappingWithTxn(
		txn, snap, userPubKey, likedPostHash)
	if existingMapping == nil {
		return nil
	}

	// When a message exists, delete the mapping for the sender and receiver.
	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForLikerPubKeyToLikedPostHashMapping(userPubKey, likedPostHash)); err != nil {
		return errors.Wrapf(err, "DbDeleteLikeMappingsWithTxn: Deleting "+
			"userPubKey %s and likedPostHash %s failed",
			PkToStringBoth(userPubKey), likedPostHash)
	}
	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForLikedPostHashToLikerPubKeyMapping(likedPostHash, userPubKey)); err != nil {
		return errors.Wrapf(err, "DbDeleteLikeMappingsWithTxn: Deleting "+
			"likedPostHash %s and userPubKey %s failed",
			PkToStringBoth(likedPostHash[:]), PkToStringBoth(userPubKey))
	}

	return nil
}

func DbDeleteLikeMappings(handle *badger.DB, snap *Snapshot,
	userPubKey []byte, likedPostHash BlockHash) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbDeleteLikeMappingsWithTxn(txn, snap, userPubKey, likedPostHash)
	})
}

func DbGetPostHashesYouLike(handle *badger.DB, yourPublicKey []byte) (
	_postHashes []*BlockHash, _err error) {

	prefix := _dbSeekPrefixForPostHashesYouLike(yourPublicKey)
	keysFound, _ := _enumerateKeysForPrefix(handle, prefix)

	postHashesYouLike := []*BlockHash{}
	for _, keyBytes := range keysFound {
		// We must slice off the first byte and userPubKey to get the likedPostHash.
		postHash := &BlockHash{}
		copy(postHash[:], keyBytes[1+btcec.PubKeyBytesLenCompressed:])
		postHashesYouLike = append(postHashesYouLike, postHash)
	}

	return postHashesYouLike, nil
}

func DbGetLikerPubKeysLikingAPostHash(handle *badger.DB, likedPostHash BlockHash) (
	_pubKeys [][]byte, _err error) {

	prefix := _dbSeekPrefixForLikerPubKeysLikingAPostHash(likedPostHash)
	keysFound, _ := _enumerateKeysForPrefix(handle, prefix)

	userPubKeys := [][]byte{}
	for _, keyBytes := range keysFound {
		// We must slice off the first byte and likedPostHash to get the userPubKey.
		userPubKey := keyBytes[1+HashSizeBytes:]
		userPubKeys = append(userPubKeys, userPubKey)
	}

	return userPubKeys, nil
}

// -------------------------------------------------------------------------------------
// Reposts mapping functions
// 		<prefix, user pub key [33]byte, reposted post BlockHash> -> <>
// 		<prefix, reposted post BlockHash, user pub key [33]byte> -> <>
// -------------------------------------------------------------------------------------
//_PrefixReposterPubKeyRepostedPostHashToRepostPostHash
func _dbKeyForReposterPubKeyRepostedPostHashToRepostPostHash(userPubKey []byte, repostedPostHash BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixReposterPubKeyRepostedPostHashToRepostPostHash...)
	key := append(prefixCopy, userPubKey...)
	key = append(key, repostedPostHash[:]...)
	return key
}

//_PrefixRepostedPostHashReposterPubKey
func _dbKeyForRepostedPostHashReposterPubKey(repostedPostHash *BlockHash, reposterPubKey []byte) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixRepostedPostHashReposterPubKey...)
	key := append(prefixCopy, repostedPostHash[:]...)
	key = append(key, reposterPubKey...)
	return key
}

// **For quoted reposts**
//_PrefixRepostedPostHashReposterPubKeyRepostPostHash
func _dbKeyForRepostedPostHashReposterPubKeyRepostPostHash(
	repostedPostHash *BlockHash, reposterPubKey []byte, repostPostHash *BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixRepostedPostHashReposterPubKeyRepostPostHash...)
	key := append(prefixCopy, repostedPostHash[:]...)
	key = append(key, reposterPubKey...)
	key = append(key, repostPostHash[:]...)
	return key
}

func _dbSeekPrefixForPostHashesYouRepost(yourPubKey []byte) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixReposterPubKeyRepostedPostHashToRepostPostHash...)
	return append(prefixCopy, yourPubKey...)
}

// Note that this adds a mapping for the user *and* the reposted post.
func DbPutRepostMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	userPubKey []byte, repostedPostHash BlockHash, repostEntry RepostEntry) error {

	if len(userPubKey) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutRepostMappingsWithTxn: User public key "+
			"length %d != %d", len(userPubKey), btcec.PubKeyBytesLenCompressed)
	}

	if err := DBSetWithTxn(txn, snap, _dbKeyForReposterPubKeyRepostedPostHashToRepostPostHash(
		userPubKey, repostedPostHash), repostEntry.Encode()); err != nil {

		return errors.Wrapf(
			err, "DbPutRepostMappingsWithTxn: Problem adding user to reposted post mapping: ")
	}

	return nil
}

func DbPutRepostMappings(handle *badger.DB, snap *Snapshot,
	userPubKey []byte, repostedPostHash BlockHash, repostEntry RepostEntry) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutRepostMappingsWithTxn(txn, snap, userPubKey, repostedPostHash, repostEntry)
	})
}

func DbGetReposterPubKeyRepostedPostHashToRepostEntryWithTxn(txn *badger.Txn,
	snap *Snapshot, userPubKey []byte, repostedPostHash BlockHash) *RepostEntry {

	key := _dbKeyForReposterPubKeyRepostedPostHashToRepostPostHash(userPubKey, repostedPostHash)
	repostEntryBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	repostEntryObj := &RepostEntry{}
	repostEntryObj.Decode(repostEntryBytes)
	return repostEntryObj
}

func DbReposterPubKeyRepostedPostHashToRepostEntry(db *badger.DB,
	snap *Snapshot, userPubKey []byte, repostedPostHash BlockHash) *RepostEntry {

	var ret *RepostEntry
	db.View(func(txn *badger.Txn) error {
		ret = DbGetReposterPubKeyRepostedPostHashToRepostEntryWithTxn(txn, snap, userPubKey, repostedPostHash)
		return nil
	})
	return ret
}

// Note this deletes the repost for the user *and* the reposted post since a mapping
// should exist for each.
func DbDeleteRepostMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	userPubKey []byte, repostedPostHash BlockHash) error {

	// First check that a mapping exists. If one doesn't exist then there's nothing to do.
	existingMapping := DbGetReposterPubKeyRepostedPostHashToRepostEntryWithTxn(
		txn, snap, userPubKey, repostedPostHash)
	if existingMapping == nil {
		return nil
	}

	// When a repost exists, delete the repost entry mapping.
	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForReposterPubKeyRepostedPostHashToRepostPostHash(userPubKey, repostedPostHash)); err != nil {
		return errors.Wrapf(err, "DbDeleteRepostMappingsWithTxn: Deleting "+
			"user public key %s and reposted post hash %s failed",
			PkToStringMainnet(userPubKey[:]), PkToStringMainnet(repostedPostHash[:]))
	}
	return nil
}

func DbDeleteRepostMappings(handle *badger.DB, snap *Snapshot,
	userPubKey []byte, repostedPostHash BlockHash) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbDeleteRepostMappingsWithTxn(txn, snap, userPubKey, repostedPostHash)
	})
}

func DbGetPostHashesYouRepost(handle *badger.DB, yourPublicKey []byte) (
	_postHashes []*BlockHash, _err error) {

	prefix := _dbSeekPrefixForPostHashesYouRepost(yourPublicKey)
	keysFound, _ := _enumerateKeysForPrefix(handle, prefix)

	postHashesYouRepost := []*BlockHash{}
	for _, keyBytes := range keysFound {
		// We must slice off the first byte and userPubKey to get the repostedPostHash.
		postHash := &BlockHash{}
		copy(postHash[:], keyBytes[1+btcec.PubKeyBytesLenCompressed:])
		postHashesYouRepost = append(postHashesYouRepost, postHash)
	}

	return postHashesYouRepost, nil
}

// -------------------------------------------------------------------------------------
// Follows mapping functions
// 		<prefix, follower pub key [33]byte, followed pub key [33]byte> -> <>
// 		<prefix, followed pub key [33]byte, follower pub key [33]byte> -> <>
// -------------------------------------------------------------------------------------

func _dbKeyForFollowerToFollowedMapping(
	followerPKID *PKID, followedPKID *PKID) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixFollowerPKIDToFollowedPKID...)
	key := append(prefixCopy, followerPKID[:]...)
	key = append(key, followedPKID[:]...)
	return key
}

func _dbKeyForFollowedToFollowerMapping(
	followedPKID *PKID, followerPKID *PKID) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixFollowedPKIDToFollowerPKID...)
	key := append(prefixCopy, followedPKID[:]...)
	key = append(key, followerPKID[:]...)
	return key
}

func _dbSeekPrefixForPKIDsYouFollow(yourPKID *PKID) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixFollowerPKIDToFollowedPKID...)
	return append(prefixCopy, yourPKID[:]...)
}

func _dbSeekPrefixForPKIDsFollowingYou(yourPKID *PKID) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixFollowedPKIDToFollowerPKID...)
	return append(prefixCopy, yourPKID[:]...)
}

// Note that this adds a mapping for the follower *and* the pub key being followed.
func DbPutFollowMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	followerPKID *PKID, followedPKID *PKID) error {

	if len(followerPKID) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutFollowMappingsWithTxn: Follower PKID "+
			"length %d != %d", len(followerPKID[:]), btcec.PubKeyBytesLenCompressed)
	}
	if len(followedPKID) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutFollowMappingsWithTxn: Followed PKID "+
			"length %d != %d", len(followerPKID), btcec.PubKeyBytesLenCompressed)
	}

	if err := DBSetWithTxn(txn, snap, _dbKeyForFollowerToFollowedMapping(
		followerPKID, followedPKID), []byte{}); err != nil {

		return errors.Wrapf(
			err, "DbPutFollowMappingsWithTxn: Problem adding follower to followed mapping: ")
	}
	if err := DBSetWithTxn(txn, snap, _dbKeyForFollowedToFollowerMapping(
		followedPKID, followerPKID), []byte{}); err != nil {

		return errors.Wrapf(
			err, "DbPutFollowMappingsWithTxn: Problem adding followed to follower mapping: ")
	}

	return nil
}

func DbPutFollowMappings(handle *badger.DB, snap *Snapshot,
	followerPKID *PKID, followedPKID *PKID) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutFollowMappingsWithTxn(txn, snap, followerPKID, followedPKID)
	})
}

func DbGetFollowerToFollowedMappingWithTxn(txn *badger.Txn,
	snap *Snapshot, followerPKID *PKID, followedPKID *PKID) []byte {

	key := _dbKeyForFollowerToFollowedMapping(followerPKID, followedPKID)
	_, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	// Typically we return a DB entry here but we don't store anything for like mappings.
	// We use this function instead of one returning true / false for feature consistency.
	return []byte{}
}

func DbGetFollowerToFollowedMapping(db *badger.DB, snap *Snapshot,
	followerPKID *PKID, followedPKID *PKID) []byte {

	var ret []byte
	db.View(func(txn *badger.Txn) error {
		ret = DbGetFollowerToFollowedMappingWithTxn(txn, snap, followerPKID, followedPKID)
		return nil
	})
	return ret
}

// Note this deletes the follow for the follower *and* followed since a mapping
// should exist for each.
func DbDeleteFollowMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	followerPKID *PKID, followedPKID *PKID) error {

	// First check that a mapping exists for the PKIDs passed in.
	// If one doesn't exist then there's nothing to do.
	existingMapping := DbGetFollowerToFollowedMappingWithTxn(
		txn, snap, followerPKID, followedPKID)
	if existingMapping == nil {
		return nil
	}

	// When a message exists, delete the mapping for the sender and receiver.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForFollowerToFollowedMapping(followerPKID, followedPKID)); err != nil {
		return errors.Wrapf(err, "DbDeleteFollowMappingsWithTxn: Deleting "+
			"followerPKID %s and followedPKID %s failed",
			PkToStringMainnet(followerPKID[:]), PkToStringMainnet(followedPKID[:]))
	}
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForFollowedToFollowerMapping(followedPKID, followerPKID)); err != nil {
		return errors.Wrapf(err, "DbDeleteFollowMappingsWithTxn: Deleting "+
			"followedPKID %s and followerPKID %s failed",
			PkToStringMainnet(followedPKID[:]), PkToStringMainnet(followerPKID[:]))
	}

	return nil
}

func DbDeleteFollowMappings(handle *badger.DB, snap *Snapshot,
	followerPKID *PKID, followedPKID *PKID) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbDeleteFollowMappingsWithTxn(txn, snap, followerPKID, followedPKID)
	})
}

func DbGetPKIDsYouFollow(handle *badger.DB, yourPKID *PKID) (
	_pkids []*PKID, _err error) {

	prefix := _dbSeekPrefixForPKIDsYouFollow(yourPKID)
	keysFound, _ := _enumerateKeysForPrefix(handle, prefix)

	pkidsYouFollow := []*PKID{}
	for _, keyBytes := range keysFound {
		// We must slice off the first byte and followerPKID to get the followedPKID.
		followedPKIDBytes := keyBytes[1+btcec.PubKeyBytesLenCompressed:]
		followedPKID := &PKID{}
		copy(followedPKID[:], followedPKIDBytes)
		pkidsYouFollow = append(pkidsYouFollow, followedPKID)
	}

	return pkidsYouFollow, nil
}

func DbGetPKIDsFollowingYou(handle *badger.DB, yourPKID *PKID) (
	_pkids []*PKID, _err error) {

	prefix := _dbSeekPrefixForPKIDsFollowingYou(yourPKID)
	keysFound, _ := _enumerateKeysForPrefix(handle, prefix)

	pkidsFollowingYou := []*PKID{}
	for _, keyBytes := range keysFound {
		// We must slice off the first byte and followedPKID to get the followerPKID.
		followerPKIDBytes := keyBytes[1+btcec.PubKeyBytesLenCompressed:]
		followerPKID := &PKID{}
		copy(followerPKID[:], followerPKIDBytes)
		pkidsFollowingYou = append(pkidsFollowingYou, followerPKID)
	}

	return pkidsFollowingYou, nil
}

func DbGetPubKeysYouFollow(handle *badger.DB, snap *Snapshot, yourPubKey []byte) (
	_pubKeys [][]byte, _err error) {

	// Get the PKID for the pub key
	yourPKID := DBGetPKIDEntryForPublicKey(handle, snap, yourPubKey)
	followPKIDs, err := DbGetPKIDsYouFollow(handle, yourPKID.PKID)
	if err != nil {
		return nil, errors.Wrap(err, "DbGetPubKeysYouFollow: ")
	}

	// Convert the pkids to public keys
	followPubKeys := [][]byte{}
	for _, fpkid := range followPKIDs {
		followPk := DBGetPublicKeyForPKID(handle, snap, fpkid)
		followPubKeys = append(followPubKeys, followPk)
	}

	return followPubKeys, nil
}

func DbGetPubKeysFollowingYou(handle *badger.DB, snap *Snapshot, yourPubKey []byte) (
	_pubKeys [][]byte, _err error) {

	// Get the PKID for the pub key
	yourPKID := DBGetPKIDEntryForPublicKey(handle, snap, yourPubKey)
	followPKIDs, err := DbGetPKIDsFollowingYou(handle, yourPKID.PKID)
	if err != nil {
		return nil, errors.Wrap(err, "DbGetPubKeysFollowingYou: ")
	}

	// Convert the pkids to public keys
	followPubKeys := [][]byte{}
	for _, fpkid := range followPKIDs {
		followPk := DBGetPublicKeyForPKID(handle, snap, fpkid)
		followPubKeys = append(followPubKeys, followPk)
	}

	return followPubKeys, nil
}

// -------------------------------------------------------------------------------------
// Diamonds mapping functions
//  <prefix, DiamondReceiverPKID [33]byte, DiamondSenderPKID [33]byte, posthash> -> <[]byte{DiamondLevel}>
// -------------------------------------------------------------------------------------

func _dbKeyForDiamondReceiverToDiamondSenderMapping(diamondEntry *DiamondEntry) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixDiamondReceiverPKIDDiamondSenderPKIDPostHash...)
	key := append(prefixCopy, diamondEntry.ReceiverPKID[:]...)
	key = append(key, diamondEntry.SenderPKID[:]...)
	key = append(key, diamondEntry.DiamondPostHash[:]...)
	return key
}

func _dbKeyForDiamondReceiverToDiamondSenderMappingWithoutEntry(
	diamondReceiverPKID *PKID, diamondSenderPKID *PKID, diamondPostHash *BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixDiamondReceiverPKIDDiamondSenderPKIDPostHash...)
	key := append(prefixCopy, diamondReceiverPKID[:]...)
	key = append(key, diamondSenderPKID[:]...)
	key = append(key, diamondPostHash[:]...)
	return key
}

func _dbKeyForDiamondedPostHashDiamonderPKIDDiamondLevel(diamondEntry *DiamondEntry) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixDiamondedPostHashDiamonderPKIDDiamondLevel...)
	key := append(prefixCopy, diamondEntry.DiamondPostHash[:]...)
	key = append(key, diamondEntry.SenderPKID[:]...)
	// Diamond level is an int64 in extraData but it forced to be non-negative in consensus.
	key = append(key, EncodeUint64(uint64(diamondEntry.DiamondLevel))...)
	return key
}

func _dbSeekPrefixForPKIDsThatDiamondedYou(yourPKID *PKID) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixDiamondReceiverPKIDDiamondSenderPKIDPostHash...)
	return append(prefixCopy, yourPKID[:]...)
}

func _dbKeyForDiamondSenderToDiamondReceiverMapping(diamondEntry *DiamondEntry) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixDiamondSenderPKIDDiamondReceiverPKIDPostHash...)
	key := append(prefixCopy, diamondEntry.SenderPKID[:]...)
	key = append(key, diamondEntry.ReceiverPKID[:]...)
	key = append(key, diamondEntry.DiamondPostHash[:]...)
	return key
}

func _dbKeyForDiamondSenderToDiamondReceiverMappingWithoutEntry(
	diamondReceiverPKID *PKID, diamondSenderPKID *PKID, diamondPostHash *BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixDiamondSenderPKIDDiamondReceiverPKIDPostHash...)
	key := append(prefixCopy, diamondSenderPKID[:]...)
	key = append(key, diamondReceiverPKID[:]...)
	key = append(key, diamondPostHash[:]...)
	return key
}

func _dbSeekPrefixForPKIDsThatYouDiamonded(yourPKID *PKID) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixDiamondSenderPKIDDiamondReceiverPKIDPostHash...)
	return append(prefixCopy, yourPKID[:]...)
}

func _dbSeekPrefixForReceiverPKIDAndSenderPKID(receiverPKID *PKID, senderPKID *PKID) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixDiamondReceiverPKIDDiamondSenderPKIDPostHash...)
	key := append(prefixCopy, receiverPKID[:]...)
	return append(key, senderPKID[:]...)
}

func DbPutDiamondMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	diamondEntry *DiamondEntry) error {

	if len(diamondEntry.ReceiverPKID) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutDiamondMappingsWithTxn: Receiver PKID "+
			"length %d != %d", len(diamondEntry.ReceiverPKID[:]), btcec.PubKeyBytesLenCompressed)
	}
	if len(diamondEntry.SenderPKID) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DbPutDiamondMappingsWithTxn: Sender PKID "+
			"length %d != %d", len(diamondEntry.SenderPKID), btcec.PubKeyBytesLenCompressed)
	}

	diamondEntryBytes := diamondEntry.Encode()
	if err := DBSetWithTxn(txn, snap, _dbKeyForDiamondReceiverToDiamondSenderMapping(diamondEntry), diamondEntryBytes); err != nil {
		return errors.Wrapf(
			err, "DbPutDiamondMappingsWithTxn: Problem adding receiver to giver mapping: ")
	}

	if err := DBSetWithTxn(txn, snap, _dbKeyForDiamondSenderToDiamondReceiverMapping(diamondEntry), diamondEntryBytes); err != nil {
		return errors.Wrapf(err, "DbPutDiamondMappingsWithTxn: Problem adding sender to receiver mapping: ")
	}

	if err := DBSetWithTxn(txn, snap, _dbKeyForDiamondedPostHashDiamonderPKIDDiamondLevel(diamondEntry),
		[]byte{}); err != nil {
		return errors.Wrapf(
			err, "DbPutDiamondMappingsWithTxn: Problem adding DiamondedPostHash Diamonder Diamond Level mapping: ")
	}

	return nil
}

func DbPutDiamondMappings(handle *badger.DB, snap *Snapshot,
	diamondEntry *DiamondEntry) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutDiamondMappingsWithTxn(txn, snap, diamondEntry)
	})
}

func DbGetDiamondMappingsWithTxn(txn *badger.Txn, snap *Snapshot, diamondReceiverPKID *PKID,
	diamondSenderPKID *PKID, diamondPostHash *BlockHash) *DiamondEntry {

	key := _dbKeyForDiamondReceiverToDiamondSenderMappingWithoutEntry(
		diamondReceiverPKID, diamondSenderPKID, diamondPostHash)
	diamondEntryBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	// We return the byte array stored for this diamond mapping. This mapping should only
	// hold one uint8 with a value between 1 and 5 but the caller is responsible for sanity
	// checking in order to maintain consistency with other DB functions that do not error.
	diamondEntry := &DiamondEntry{}
	diamondEntry.Decode(diamondEntryBytes)
	return diamondEntry
}

func DbGetDiamondMappings(db *badger.DB, snap *Snapshot, diamondReceiverPKID *PKID,
	diamondSenderPKID *PKID, diamondPostHash *BlockHash) *DiamondEntry {

	var ret *DiamondEntry
	db.View(func(txn *badger.Txn) error {
		ret = DbGetDiamondMappingsWithTxn(
			txn, snap, diamondReceiverPKID, diamondSenderPKID, diamondPostHash)
		return nil
	})
	return ret
}

func DbDeleteDiamondMappingsWithTxn(txn *badger.Txn, snap *Snapshot, diamondEntry *DiamondEntry) error {

	// First check that a mapping exists for the PKIDs passed in.
	// If one doesn't exist then there's nothing to do.
	existingMapping := DbGetDiamondMappingsWithTxn(txn, snap,
		diamondEntry.ReceiverPKID, diamondEntry.SenderPKID, diamondEntry.DiamondPostHash)
	if existingMapping == nil {
		return nil
	}

	// When a DiamondEntry exists, delete the diamond mappings.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForDiamondReceiverToDiamondSenderMapping(diamondEntry)); err != nil {
		return errors.Wrapf(err, "DbDeleteDiamondMappingsWithTxn: Deleting "+
			"diamondReceiverPKID %s and diamondSenderPKID %s and diamondPostHash %s failed",
			PkToStringMainnet(diamondEntry.ReceiverPKID[:]),
			PkToStringMainnet(diamondEntry.SenderPKID[:]),
			diamondEntry.DiamondPostHash.String(),
		)
	}
	// When a DiamondEntry exists, delete the diamond mappings.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForDiamondedPostHashDiamonderPKIDDiamondLevel(diamondEntry)); err != nil {
		return errors.Wrapf(err, "DbDeleteDiamondMappingsWithTxn: Deleting "+
			"diamondedPostHash %s and diamonderPKID %s and diamondLevel %s failed",
			diamondEntry.DiamondPostHash.String(),
			PkToStringMainnet(diamondEntry.SenderPKID[:]),
			diamondEntry.DiamondPostHash.String(),
		)
	}

	if err := DBDeleteWithTxn(txn, snap, _dbKeyForDiamondSenderToDiamondReceiverMapping(diamondEntry)); err != nil {
		return errors.Wrapf(err, "DbDeleteDiamondMappingsWithTxn: Deleting "+
			"diamondSenderPKID %s and diamondReceiverPKID %s and diamondPostHash %s failed",
			PkToStringMainnet(diamondEntry.SenderPKID[:]),
			PkToStringMainnet(diamondEntry.ReceiverPKID[:]),
			diamondEntry.DiamondPostHash.String(),
		)
	}

	return nil
}

func DbDeleteDiamondMappings(handle *badger.DB, snap *Snapshot, diamondEntry *DiamondEntry) error {
	return handle.Update(func(txn *badger.Txn) error {
		return DbDeleteDiamondMappingsWithTxn(txn, snap, diamondEntry)
	})
}

// This function returns a map of PKIDs that gave diamonds to a list of DiamondEntrys
// that contain post hashes.
func DbGetPKIDsThatDiamondedYouMap(handle *badger.DB, yourPKID *PKID, fetchYouDiamonded bool) (
	_pkidToDiamondsMap map[PKID][]*DiamondEntry, _err error) {

	prefix := _dbSeekPrefixForPKIDsThatDiamondedYou(yourPKID)
	diamondSenderStartIdx := 1 + btcec.PubKeyBytesLenCompressed
	diamondSenderEndIdx := 1 + 2*btcec.PubKeyBytesLenCompressed
	diamondReceiverStartIdx := 1
	diamondReceiverEndIdx := 1 + btcec.PubKeyBytesLenCompressed
	if fetchYouDiamonded {
		prefix = _dbSeekPrefixForPKIDsThatYouDiamonded(yourPKID)
		diamondSenderStartIdx = 1
		diamondSenderEndIdx = 1 + btcec.PubKeyBytesLenCompressed
		diamondReceiverStartIdx = 1 + btcec.PubKeyBytesLenCompressed
		diamondReceiverEndIdx = 1 + 2*btcec.PubKeyBytesLenCompressed
	}
	keysFound, valsFound := _enumerateKeysForPrefix(handle, prefix)

	pkidsToDiamondEntryMap := make(map[PKID][]*DiamondEntry)
	for ii, keyBytes := range keysFound {
		// The DiamondEntry found must not be nil.
		diamondEntry := &DiamondEntry{}
		diamondEntry.Decode(valsFound[ii])
		if diamondEntry == nil {
			return nil, fmt.Errorf(
				"DbGetPKIDsThatDiamondedYouMap: Found nil DiamondEntry for public key %v "+
					"and key bytes %#v when seeking; this should never happen",
				PkToStringMainnet(yourPKID[:]), keyBytes)
		}
		expectedDiamondKeyLen := 1 + 2*btcec.PubKeyBytesLenCompressed + HashSizeBytes
		if len(keyBytes) != expectedDiamondKeyLen {
			return nil, fmt.Errorf(
				"DbGetPKIDsThatDiamondedYouMap: Invalid key length %v should be %v",
				len(keyBytes), expectedDiamondKeyLen)
		}

		// Note: The code below is mainly just sanity-checking. Checking the key isn't actually
		// needed in this function, since all the information is duplicated in the entry.

		// Chop out the diamond sender PKID.
		diamondSenderPKIDBytes := keyBytes[diamondSenderStartIdx:diamondSenderEndIdx]
		diamondSenderPKID := &PKID{}
		copy(diamondSenderPKID[:], diamondSenderPKIDBytes)
		// It must match what's in the DiamondEntry
		if !reflect.DeepEqual(diamondSenderPKID, diamondEntry.SenderPKID) {
			return nil, fmt.Errorf(
				"DbGetPKIDsThatDiamondedYouMap: Sender PKID in DB %v did not "+
					"match Sender PKID in DiamondEntry %v; this should never happen",
				PkToStringBoth(diamondSenderPKID[:]), PkToStringBoth(diamondEntry.SenderPKID[:]))
		}

		// Chop out the diamond receiver PKID
		diamondReceiverPKIDBytes := keyBytes[diamondReceiverStartIdx:diamondReceiverEndIdx]
		diamondReceiverPKID := &PKID{}
		copy(diamondReceiverPKID[:], diamondReceiverPKIDBytes)
		// It must match what's in the DiamondEntry
		if !reflect.DeepEqual(diamondReceiverPKID, diamondEntry.ReceiverPKID) {
			return nil, fmt.Errorf(
				"DbGetPKIDsThatDiamondedYouMap: Receiver PKID in DB %v did not "+
					"match Receiver PKID in DiamondEntry %v; this should never happen",
				PkToStringBoth(diamondReceiverPKID[:]), PkToStringBoth(diamondEntry.ReceiverPKID[:]))
		}

		// Chop out the diamond post hash.
		diamondPostHashBytes := keyBytes[1+2*btcec.PubKeyBytesLenCompressed:]
		diamondPostHash := &BlockHash{}
		copy(diamondPostHash[:], diamondPostHashBytes)
		// It must match what's in the entry
		if *diamondPostHash != *diamondEntry.DiamondPostHash {
			return nil, fmt.Errorf(
				"DbGetPKIDsThatDiamondedYouMap: Post hash found in DB key %v "+
					"did not match post hash in DiamondEntry %v; this should never happen",
				diamondPostHash, diamondEntry.DiamondPostHash)
		}

		// If a map entry doesn't exist for this sender, create one.
		newListOfEntrys := pkidsToDiamondEntryMap[*diamondSenderPKID]
		newListOfEntrys = append(newListOfEntrys, diamondEntry)
		pkidsToDiamondEntryMap[*diamondSenderPKID] = newListOfEntrys
	}

	return pkidsToDiamondEntryMap, nil
}

// This function returns a list of DiamondEntrys given by giverPKID to receiverPKID that contain post hashes.
func DbGetDiamondEntriesForSenderToReceiver(handle *badger.DB, receiverPKID *PKID, senderPKID *PKID) (
	_diamondEntries []*DiamondEntry, _err error) {

	prefix := _dbSeekPrefixForReceiverPKIDAndSenderPKID(receiverPKID, senderPKID)
	keysFound, valsFound := _enumerateKeysForPrefix(handle, prefix)
	var diamondEntries []*DiamondEntry
	for ii, keyBytes := range keysFound {
		// The DiamondEntry found must not be nil.
		diamondEntry := &DiamondEntry{}
		diamondEntry.Decode(valsFound[ii])
		if diamondEntry == nil {
			return nil, fmt.Errorf(
				"DbGetDiamondEntriesForGiverToReceiver: Found nil DiamondEntry for receiver key %v "+
					"and giver key %v when seeking; this should never happen",
				PkToStringMainnet(receiverPKID[:]), PkToStringMainnet(senderPKID[:]))
		}
		expectedDiamondKeyLen := 1 + 2*btcec.PubKeyBytesLenCompressed + HashSizeBytes
		if len(keyBytes) != expectedDiamondKeyLen {
			return nil, fmt.Errorf(
				"DbGetDiamondEntriesForGiverToReceiver: Invalid key length %v should be %v",
				len(keyBytes), expectedDiamondKeyLen)
		}

		// Note: The code below is mainly just sanity-checking. Checking the key isn't actually
		// needed in this function, since all the information is duplicated in the entry.

		// Chop out the diamond sender PKID.
		diamondSenderPKIDBytes := keyBytes[1+btcec.PubKeyBytesLenCompressed : 1+2*btcec.PubKeyBytesLenCompressed]
		diamondSenderPKID := &PKID{}
		copy(diamondSenderPKID[:], diamondSenderPKIDBytes)
		// It must match what's in the DiamondEntry
		if !reflect.DeepEqual(diamondSenderPKID, diamondEntry.SenderPKID) {
			return nil, fmt.Errorf(
				"DbGetDiamondEntriesForGiverToReceiver: Sender PKID in DB %v did not "+
					"match Sender PKID in DiamondEntry %v; this should never happen",
				PkToStringBoth(diamondSenderPKID[:]), PkToStringBoth(diamondEntry.SenderPKID[:]))
		}

		// Chop out the diamond post hash.
		diamondPostHashBytes := keyBytes[1+2*btcec.PubKeyBytesLenCompressed:]
		diamondPostHash := &BlockHash{}
		copy(diamondPostHash[:], diamondPostHashBytes)
		// It must match what's in the entry
		if *diamondPostHash != *diamondEntry.DiamondPostHash {
			return nil, fmt.Errorf(
				"DbGetDiamondEntriesForGiverToReceiver: Post hash found in DB key %v "+
					"did not match post hash in DiamondEntry %v; this should never happen",
				diamondPostHash, diamondEntry.DiamondPostHash)
		}
		// Append the diamond entry to the slice
		diamondEntries = append(diamondEntries, diamondEntry)
	}
	return diamondEntries, nil
}

// -------------------------------------------------------------------------------------
// BitcoinBurnTxID mapping functions
// <BitcoinBurnTxID BlockHash> -> <>
// -------------------------------------------------------------------------------------

func _keyForBitcoinBurnTxID(bitcoinBurnTxID *BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same
	// underlying array.
	prefixCopy := append([]byte{}, _PrefixBitcoinBurnTxIDs...)
	return append(prefixCopy, bitcoinBurnTxID[:]...)
}

func DbPutBitcoinBurnTxIDWithTxn(txn *badger.Txn, snap *Snapshot, bitcoinBurnTxID *BlockHash) error {
	return DBSetWithTxn(txn, snap, _keyForBitcoinBurnTxID(bitcoinBurnTxID), []byte{})
}

func DbExistsBitcoinBurnTxIDWithTxn(txn *badger.Txn, snap *Snapshot, bitcoinBurnTxID *BlockHash) bool {
	// We don't care about the value because we're just checking to see if the key exists.
	if _, err := DBGetWithTxn(txn, snap, _keyForBitcoinBurnTxID(bitcoinBurnTxID)); err != nil {
		return false
	}
	return true
}

func DbExistsBitcoinBurnTxID(db *badger.DB, snap *Snapshot, bitcoinBurnTxID *BlockHash) bool {
	var exists bool
	db.View(func(txn *badger.Txn) error {
		exists = DbExistsBitcoinBurnTxIDWithTxn(txn, snap, bitcoinBurnTxID)
		return nil
	})
	return exists
}

func DbDeleteBitcoinBurnTxIDWithTxn(txn *badger.Txn, snap *Snapshot, bitcoinBurnTxID *BlockHash) error {
	return DBDeleteWithTxn(txn, snap, _keyForBitcoinBurnTxID(bitcoinBurnTxID))
}

func DbGetAllBitcoinBurnTxIDs(handle *badger.DB) (_bitcoinBurnTxIDs []*BlockHash) {
	keysFound, _ := _enumerateKeysForPrefix(handle, _PrefixBitcoinBurnTxIDs)
	bitcoinBurnTxIDs := []*BlockHash{}
	for _, key := range keysFound {
		bbtxid := &BlockHash{}
		copy(bbtxid[:], key[1:])
		bitcoinBurnTxIDs = append(bitcoinBurnTxIDs, bbtxid)
	}

	return bitcoinBurnTxIDs
}

func _getBlockHashForPrefixWithTxn(txn *badger.Txn, snap *Snapshot, prefix []byte) *BlockHash {
	blockHash, err := DBGetWithTxn(txn, snap, prefix)
	if err != nil {
		return nil
	}

	return NewBlockHash(blockHash)
}

func _getBlockHashForPrefix(handle *badger.DB, snap *Snapshot, prefix []byte) *BlockHash {
	var ret *BlockHash
	err := handle.View(func(txn *badger.Txn) error {
		ret = _getBlockHashForPrefixWithTxn(txn, snap, prefix)
		return nil
	})
	if err != nil {
		return nil
	}
	return ret
}

// GetBadgerDbPath returns the path where we store the badgerdb data.
func GetBadgerDbPath(dataDir string) string {
	return filepath.Join(dataDir, BadgerDbFolder)
}

func _EncodeUint32(num uint32) []byte {
	numBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(numBytes, num)
	return numBytes
}

func DecodeUint32(num []byte) uint32 {
	return binary.BigEndian.Uint32(num)
}

func EncodeUint64(num uint64) []byte {
	numBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(numBytes, num)
	return numBytes
}

func DecodeUint64(scoreBytes []byte) uint64 {
	return binary.BigEndian.Uint64(scoreBytes)
}

func DbPutNanosPurchasedWithTxn(txn *badger.Txn, snap *Snapshot, nanosPurchased uint64) error {
	return DBSetWithTxn(txn, snap, _KeyNanosPurchased, EncodeUint64(nanosPurchased))
}

func DbPutNanosPurchased(handle *badger.DB, snap *Snapshot, nanosPurchased uint64) error {
	return handle.Update(func(txn *badger.Txn) error {
		return DbPutNanosPurchasedWithTxn(txn, snap, nanosPurchased)
	})
}

func DbGetNanosPurchasedWithTxn(txn *badger.Txn, snap *Snapshot) uint64 {
	nanosPurchasedBytes, err := DBGetWithTxn(txn, snap, _KeyNanosPurchased)
	if err != nil {
		return 0
	}

	return DecodeUint64(nanosPurchasedBytes)
}

func DbGetNanosPurchased(handle *badger.DB, snap *Snapshot) uint64 {
	var nanosPurchased uint64
	handle.View(func(txn *badger.Txn) error {
		nanosPurchased = DbGetNanosPurchasedWithTxn(txn, snap)
		return nil
	})

	return nanosPurchased
}

func DbPutGlobalParamsEntry(handle *badger.DB, snap *Snapshot,
	globalParamsEntry GlobalParamsEntry) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutGlobalParamsEntryWithTxn(txn, snap, globalParamsEntry)
	})
}

func DbPutGlobalParamsEntryWithTxn(txn *badger.Txn, snap *Snapshot,
	globalParamsEntry GlobalParamsEntry) error {

	err := DBSetWithTxn(txn, snap, _KeyGlobalParams, globalParamsEntry.Encode())
	if err != nil {
		return errors.Wrapf(err, "DbPutGlobalParamsEntryWithTxn: Problem adding global params entry to db: ")
	}
	return nil
}

func DbGetGlobalParamsEntryWithTxn(txn *badger.Txn, snap *Snapshot) *GlobalParamsEntry {
	globalParamsEntryBytes, err := DBGetWithTxn(txn, snap, _KeyGlobalParams)
	if err != nil {
		return &InitialGlobalParamsEntry
	}
	globalParamsEntryObj := &GlobalParamsEntry{}
	globalParamsEntryObj.Decode(globalParamsEntryBytes)

	return globalParamsEntryObj
}

func DbGetGlobalParamsEntry(handle *badger.DB, snap *Snapshot) *GlobalParamsEntry {
	var globalParamsEntry *GlobalParamsEntry
	handle.View(func(txn *badger.Txn) error {
		globalParamsEntry = DbGetGlobalParamsEntryWithTxn(txn, snap)
		return nil
	})
	return globalParamsEntry
}

func DbPutUSDCentsPerBitcoinExchangeRateWithTxn(txn *badger.Txn, snap *Snapshot,
	usdCentsPerBitcoinExchangeRate uint64) error {

	return DBSetWithTxn(txn, snap, _KeyUSDCentsPerBitcoinExchangeRate,
		EncodeUint64(usdCentsPerBitcoinExchangeRate))
}

func DbGetUSDCentsPerBitcoinExchangeRateWithTxn(txn *badger.Txn, snap *Snapshot) uint64 {
	usdCentsPerBitcoinExchangeRateBytes, err := DBGetWithTxn(txn, snap, _KeyUSDCentsPerBitcoinExchangeRate)
	if err != nil {
		return InitialUSDCentsPerBitcoinExchangeRate
	}

	return DecodeUint64(usdCentsPerBitcoinExchangeRateBytes)
}

func DbGetUSDCentsPerBitcoinExchangeRate(handle *badger.DB, snap *Snapshot) uint64 {
	var usdCentsPerBitcoinExchangeRate uint64
	handle.View(func(txn *badger.Txn) error {
		usdCentsPerBitcoinExchangeRate = DbGetUSDCentsPerBitcoinExchangeRateWithTxn(txn, snap)
		return nil
	})

	return usdCentsPerBitcoinExchangeRate
}

func GetUtxoNumEntriesWithTxn(txn *badger.Txn, snap *Snapshot) uint64 {
	indexBytes, err := DBGetWithTxn(txn, snap, _KeyUtxoNumEntries)
	if err != nil {
		return 0
	}

	return DecodeUint64(indexBytes)
}

func GetUtxoNumEntries(handle *badger.DB, snap *Snapshot) uint64 {
	var numEntries uint64
	handle.View(func(txn *badger.Txn) error {
		numEntries = GetUtxoNumEntriesWithTxn(txn, snap)
		return nil
	})

	return numEntries
}

func _SerializeUtxoKey(utxoKey *UtxoKey) []byte {
	indexBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(indexBytes, utxoKey.Index)
	return append(utxoKey.TxID[:], indexBytes...)

}

func _DbKeyForUtxoKey(utxoKey *UtxoKey) []byte {
	return append(append([]byte{}, _PrefixUtxoKeyToUtxoEntry...), _SerializeUtxoKey(utxoKey)...)
}

// Implements the reverse of _DbKeyForUtxoKey. This doesn't error-check
// and caller should make sure they're passing a properly-sized key to
// this function.
func _UtxoKeyFromDbKey(utxoDbKey []byte) *UtxoKey {
	// Read in the TxID, which is at the beginning.
	txIDBytes := utxoDbKey[:HashSizeBytes]
	txID := BlockHash{}
	copy(txID[:], txIDBytes)
	// Read in the index, which is encoded as a bigint at the end.
	indexBytes := utxoDbKey[HashSizeBytes:]
	indexValue := binary.BigEndian.Uint32(indexBytes)
	return &UtxoKey{
		Index: indexValue,
		TxID:  txID,
	}
}

func PutUtxoNumEntriesWithTxn(txn *badger.Txn, snap *Snapshot, newNumEntries uint64) error {
	return DBSetWithTxn(txn, snap, _KeyUtxoNumEntries, EncodeUint64(newNumEntries))
}

func PutUtxoEntryForUtxoKeyWithTxn(txn *badger.Txn, snap *Snapshot,
	utxoKey *UtxoKey, utxoEntry *UtxoEntry) error {

	return DBSetWithTxn(txn, snap, _DbKeyForUtxoKey(utxoKey), utxoEntry.Encode())
}

func DbGetUtxoEntryForUtxoKeyWithTxn(txn *badger.Txn, snap *Snapshot, utxoKey *UtxoKey) *UtxoEntry {
	utxoDbKey := _DbKeyForUtxoKey(utxoKey)
	utxoEntryBytes, err := DBGetWithTxn(txn, snap, utxoDbKey)
	if err != nil {
		return nil
	}

	utxoEntry := &UtxoEntry{}
	utxoEntry.Decode(utxoEntryBytes)
	return utxoEntry
}

func DbGetUtxoEntryForUtxoKey(handle *badger.DB, snap *Snapshot, utxoKey *UtxoKey) *UtxoEntry {
	var ret *UtxoEntry
	handle.View(func(txn *badger.Txn) error {
		ret = DbGetUtxoEntryForUtxoKeyWithTxn(txn, snap, utxoKey)
		return nil
	})

	return ret
}

func DeleteUtxoEntryForKeyWithTxn(txn *badger.Txn, snap *Snapshot, utxoKey *UtxoKey) error {
	return DBDeleteWithTxn(txn, snap, _DbKeyForUtxoKey(utxoKey))
}

func DeletePubKeyUtxoKeyMappingWithTxn(txn *badger.Txn, snap *Snapshot,
	publicKey []byte, utxoKey *UtxoKey) error {
	if len(publicKey) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DeletePubKeyUtxoKeyMappingWithTxn: Public key has improper length %d != %d", len(publicKey), btcec.PubKeyBytesLenCompressed)
	}

	keyToDelete := append(append([]byte{}, _PrefixPubKeyUtxoKey...), publicKey...)
	keyToDelete = append(keyToDelete, _SerializeUtxoKey(utxoKey)...)

	return DBDeleteWithTxn(txn, snap, keyToDelete)
}

func DbBufForUtxoKey(utxoKey *UtxoKey) []byte {
	utxoKeyBuf := bytes.NewBuffer([]byte{})
	gob.NewEncoder(utxoKeyBuf).Encode(utxoKey)
	return utxoKeyBuf.Bytes()
}

func PutPubKeyUtxoKeyWithTxn(txn *badger.Txn, snap *Snapshot, publicKey []byte, utxoKey *UtxoKey) error {
	if len(publicKey) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("PutPubKeyUtxoKeyWithTxn: Public key has improper length %d != %d", len(publicKey), btcec.PubKeyBytesLenCompressed)
	}

	keyToAdd := append(append([]byte{}, _PrefixPubKeyUtxoKey...), publicKey...)
	keyToAdd = append(keyToAdd, _SerializeUtxoKey(utxoKey)...)

	return DBSetWithTxn(txn, snap, keyToAdd, []byte{})
}

// DbGetUtxosForPubKey finds the UtxoEntry's corresponding to the public
// key passed in. It also attaches the UtxoKeys to the UtxoEntry's it
// returns for easy access.
func DbGetUtxosForPubKey(publicKey []byte, handle *badger.DB, snap *Snapshot) ([]*UtxoEntry, error) {
	// Verify the length of the public key.
	if len(publicKey) != btcec.PubKeyBytesLenCompressed {
		return nil, fmt.Errorf("DbGetUtxosForPubKey: Public key has improper "+
			"length %d != %d", len(publicKey), btcec.PubKeyBytesLenCompressed)
	}
	// Look up the utxo keys for this public key.
	utxoEntriesFound := []*UtxoEntry{}
	err := handle.View(func(txn *badger.Txn) error {
		// Start by looping through to find all the UtxoKeys.
		utxoKeysFound := []*UtxoKey{}
		opts := badger.DefaultIteratorOptions
		nodeIterator := txn.NewIterator(opts)
		defer nodeIterator.Close()
		prefix := append(append([]byte{}, _PrefixPubKeyUtxoKey...), publicKey...)
		for nodeIterator.Seek(prefix); nodeIterator.ValidForPrefix(prefix); nodeIterator.Next() {
			// Strip the prefix off the key. What's left should be the UtxoKey.
			pkUtxoKey := nodeIterator.Item().Key()
			utxoKeyBytes := pkUtxoKey[len(prefix):]
			// The size of the utxo key bytes should be equal to the size of a
			// standard hash (the txid) plus the size of a uint32.
			if len(utxoKeyBytes) != HashSizeBytes+4 {
				return fmt.Errorf("Problem reading <pk, utxoKey> mapping; key size %d "+
					"is not equal to (prefix_byte=%d + len(publicKey)=%d + len(utxoKey)=%d)=%d. "+
					"Key found: %#v", len(pkUtxoKey), len(_PrefixPubKeyUtxoKey), len(publicKey), HashSizeBytes+4, len(prefix)+HashSizeBytes+4, pkUtxoKey)
			}
			// Try and convert the utxo key bytes into a utxo key.
			utxoKey := _UtxoKeyFromDbKey(utxoKeyBytes)
			if utxoKey == nil {
				return fmt.Errorf("Problem reading <pk, utxoKey> mapping; parsing UtxoKey bytes %#v returned nil", utxoKeyBytes)
			}

			// Now that we have the utxoKey, enqueue it.
			utxoKeysFound = append(utxoKeysFound, utxoKey)
		}

		// Once all the UtxoKeys are found, fetch all the UtxoEntries.
		for ii := range utxoKeysFound {
			foundUtxoKey := utxoKeysFound[ii]
			utxoEntry := DbGetUtxoEntryForUtxoKeyWithTxn(txn, snap, foundUtxoKey)
			if utxoEntry == nil {
				return fmt.Errorf("UtxoEntry for UtxoKey %v was not found", foundUtxoKey)
			}

			// Set a back-reference to the utxo key.
			utxoEntry.UtxoKey = foundUtxoKey

			utxoEntriesFound = append(utxoEntriesFound, utxoEntry)
		}

		return nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "DbGetUtxosForPubKey: ")
	}

	// If there are no errors, return everything we found.
	return utxoEntriesFound, nil
}

func DeleteUnmodifiedMappingsForUtxoWithTxn(txn *badger.Txn, snap *Snapshot, utxoKey *UtxoKey) error {
	// Get the entry for the utxoKey from the db.
	utxoEntry := DbGetUtxoEntryForUtxoKeyWithTxn(txn, snap, utxoKey)
	if utxoEntry == nil {
		// If an entry doesn't exist for this key then there is nothing in the
		// db to delete.
		return nil
	}

	// If the entry exists, delete the <UtxoKey -> UtxoEntry> mapping from the db.
	// It is assumed that the entry corresponding to a key has not been modified
	// and so is OK to delete
	if err := DeleteUtxoEntryForKeyWithTxn(txn, snap, utxoKey); err != nil {
		return err
	}

	// Delete the <pubkey, utxoKey> -> <> mapping.
	if err := DeletePubKeyUtxoKeyMappingWithTxn(txn, snap, utxoEntry.PublicKey, utxoKey); err != nil {
		return err
	}

	return nil
}

func PutMappingsForUtxoWithTxn(txn *badger.Txn, snap *Snapshot, utxoKey *UtxoKey, utxoEntry *UtxoEntry) error {
	// Put the <utxoKey -> utxoEntry> mapping.
	if err := PutUtxoEntryForUtxoKeyWithTxn(txn, snap, utxoKey, utxoEntry); err != nil {
		return nil
	}

	// Put the <pubkey, utxoKey> -> <> mapping.
	if err := PutPubKeyUtxoKeyWithTxn(txn, snap, utxoEntry.PublicKey, utxoKey); err != nil {
		return err
	}

	return nil
}

func _DecodeUtxoOperations(data []byte) ([][]*UtxoOperation, error) {
	ret := [][]*UtxoOperation{}
	// TODO: Custom encoder here?
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&ret); err != nil {
		return nil, err
	}
	return ret, nil
}

func _EncodeUtxoOperations(utxoOp [][]*UtxoOperation) []byte {
	opBuf := bytes.NewBuffer([]byte{})
	// TODO: Custom encoder here?
	gob.NewEncoder(opBuf).Encode(utxoOp)
	return opBuf.Bytes()
}

func _DbKeyForUtxoOps(blockHash *BlockHash) []byte {
	return append(append([]byte{}, _PrefixBlockHashToUtxoOperations...), blockHash[:]...)
}

func GetUtxoOperationsForBlockWithTxn(txn *badger.Txn, snap *Snapshot, blockHash *BlockHash) ([][]*UtxoOperation, error) {
	utxoOpsBytes, err := DBGetWithTxn(txn, snap, _DbKeyForUtxoOps(blockHash))
	if err != nil {
		return nil, err
	}

	var retOps [][]*UtxoOperation
	retOps, err = _DecodeUtxoOperations(utxoOpsBytes)
	if err != nil {
		return nil, err
	}

	return retOps, err
}

func GetUtxoOperationsForBlock(handle *badger.DB, snap *Snapshot, blockHash *BlockHash) ([][]*UtxoOperation, error) {
	var ops [][]*UtxoOperation
	err := handle.View(func(txn *badger.Txn) error {
		var err error
		ops, err = GetUtxoOperationsForBlockWithTxn(txn, snap, blockHash)
		return err
	})

	return ops, err
}

func PutUtxoOperationsForBlockWithTxn(txn *badger.Txn, snap *Snapshot,
	blockHash *BlockHash, utxoOpsForBlock [][]*UtxoOperation) error {

	return DBSetWithTxn(txn, snap, _DbKeyForUtxoOps(blockHash), _EncodeUtxoOperations(utxoOpsForBlock))
}

func DeleteUtxoOperationsForBlockWithTxn(txn *badger.Txn, snap *Snapshot, blockHash *BlockHash) error {
	return DBDeleteWithTxn(txn, snap, _DbKeyForUtxoOps(blockHash))
}

func SerializeBlockNode(blockNode *BlockNode) ([]byte, error) {
	data := []byte{}

	// Hash
	if blockNode.Hash == nil {
		return nil, fmt.Errorf("SerializeBlockNode: Hash cannot be nil")
	}
	data = append(data, blockNode.Hash[:]...)

	// Height
	data = append(data, UintToBuf(uint64(blockNode.Height))...)

	// DifficultyTarget
	if blockNode.DifficultyTarget == nil {
		return nil, fmt.Errorf("SerializeBlockNode: DifficultyTarget cannot be nil")
	}
	data = append(data, blockNode.DifficultyTarget[:]...)

	// CumWork
	data = append(data, BigintToHash(blockNode.CumWork)[:]...)

	// Header
	serializedHeader, err := blockNode.Header.ToBytes(false)
	if err != nil {
		return nil, errors.Wrapf(err, "SerializeBlockNode: Problem serializing header")
	}
	data = append(data, IntToBuf(int64(len(serializedHeader)))...)
	data = append(data, serializedHeader...)

	// Status
	// It's assumed this field is one byte long.
	data = append(data, UintToBuf(uint64(blockNode.Status))...)

	return data, nil
}

func DeserializeBlockNode(data []byte) (*BlockNode, error) {
	blockNode := NewBlockNode(
		nil,          // Parent
		&BlockHash{}, // Hash
		0,            // Height
		&BlockHash{}, // DifficultyTarget
		nil,          // CumWork
		nil,          // Header
		StatusNone,   // Status

	)

	rr := bytes.NewReader(data)

	// Hash
	_, err := io.ReadFull(rr, blockNode.Hash[:])
	if err != nil {
		return nil, errors.Wrapf(err, "DeserializeBlockNode: Problem decoding Hash")
	}

	// Height
	height, err := ReadUvarint(rr)
	if err != nil {
		return nil, errors.Wrapf(err, "DeserializeBlockNode: Problem decoding Height")
	}
	blockNode.Height = uint32(height)

	// DifficultyTarget
	_, err = io.ReadFull(rr, blockNode.DifficultyTarget[:])
	if err != nil {
		return nil, errors.Wrapf(err, "DeserializeBlockNode: Problem decoding DifficultyTarget")
	}

	// CumWork
	tmp := BlockHash{}
	_, err = io.ReadFull(rr, tmp[:])
	if err != nil {
		return nil, errors.Wrapf(err, "DeserializeBlockNode: Problem decoding CumWork")
	}
	blockNode.CumWork = HashToBigint(&tmp)

	// Header
	payloadLen, err := ReadVarint(rr)
	if err != nil {
		return nil, errors.Wrapf(err, "DeserializeBlockNode: Problem decoding Header length")
	}
	headerBytes := make([]byte, payloadLen)
	_, err = io.ReadFull(rr, headerBytes[:])
	if err != nil {
		return nil, errors.Wrapf(err, "DeserializeBlockNode: Problem reading Header bytes")
	}
	blockNode.Header = NewMessage(MsgTypeHeader).(*MsgDeSoHeader)
	err = blockNode.Header.FromBytes(headerBytes)
	if err != nil {
		return nil, errors.Wrapf(err, "DeserializeBlockNode: Problem parsing Header bytes")
	}

	// Status
	status, err := ReadUvarint(rr)
	if err != nil {
		return nil, errors.Wrapf(err, "DeserializeBlockNode: Problem decoding Status")
	}
	blockNode.Status = BlockStatus(uint32(status))

	return blockNode, nil
}

type ChainType uint8

const (
	ChainTypeDeSoBlock = iota
	ChainTypeBitcoinHeader
)

func _prefixForChainType(chainType ChainType) []byte {
	var prefix []byte
	switch chainType {
	case ChainTypeDeSoBlock:
		prefix = _KeyBestDeSoBlockHash
	case ChainTypeBitcoinHeader:
		prefix = _KeyBestBitcoinHeaderHash
	default:
		glog.Errorf("_prefixForChainType: Unknown ChainType %d; this should never happen", chainType)
		return nil
	}

	return prefix
}

func DbGetBestHash(handle *badger.DB, snap *Snapshot, chainType ChainType) *BlockHash {
	prefix := _prefixForChainType(chainType)
	if len(prefix) == 0 {
		glog.Errorf("DbGetBestHash: Problem getting prefix for ChainType: %d", chainType)
		return nil
	}
	return _getBlockHashForPrefix(handle, snap, prefix)
}

func PutBestHashWithTxn(txn *badger.Txn, snap *Snapshot,
	bh *BlockHash, chainType ChainType) error {

	prefix := _prefixForChainType(chainType)
	if len(prefix) == 0 {
		glog.Errorf("PutBestHashWithTxn: Problem getting prefix for ChainType: %d", chainType)
		return nil
	}
	return DBSetWithTxn(txn, snap, prefix, bh[:])
}

func PutBestHash(handle *badger.DB, snap *Snapshot, bh *BlockHash, chainType ChainType) error {
	return handle.Update(func(txn *badger.Txn) error {
		return PutBestHashWithTxn(txn, snap, bh, chainType)
	})
}

func BlockHashToBlockKey(blockHash *BlockHash) []byte {
	return append(append([]byte{}, _PrefixBlockHashToBlock...), blockHash[:]...)
}

func PublicKeyBlockHashToBlockRewardKey(publicKey []byte, blockHash *BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixPublicKeyBlockHashToBlockReward...)
	key := append(prefixCopy, publicKey...)
	key = append(key, blockHash[:]...)
	return key
}

func GetBlockWithTxn(txn *badger.Txn, snap *Snapshot, blockHash *BlockHash) *MsgDeSoBlock {
	hashKey := BlockHashToBlockKey(blockHash)

	blockBytes, err := DBGetWithTxn(txn, snap, hashKey)
	if err != nil {
		return nil
	}

	blockRet := NewMessage(MsgTypeBlock).(*MsgDeSoBlock)
	if err := blockRet.FromBytes(blockBytes); err != nil {
		return nil
	}

	return blockRet
}

func GetBlock(blockHash *BlockHash, handle *badger.DB, snap *Snapshot) (*MsgDeSoBlock, error) {
	hashKey := BlockHashToBlockKey(blockHash)
	var blockRet *MsgDeSoBlock
	err := handle.View(func(txn *badger.Txn) error {
		blockBytes, err := DBGetWithTxn(txn, snap, hashKey)
		if err != nil {
			return err
		}

		ret := NewMessage(MsgTypeBlock).(*MsgDeSoBlock)
		if err := ret.FromBytes(blockBytes); err != nil {
			return err
		}
		blockRet = ret
		return nil
	})
	if err != nil {
		return nil, err
	}

	return blockRet, nil
}

func PutBlockWithTxn(txn *badger.Txn, snap *Snapshot, desoBlock *MsgDeSoBlock) error {
	if desoBlock.Header == nil {
		return fmt.Errorf("PutBlockWithTxn: Header was nil in block %v", desoBlock)
	}
	blockHash, err := desoBlock.Header.Hash()
	if err != nil {
		return errors.Wrapf(err, "PutBlockWithTxn: Problem hashing header: ")
	}
	blockKey := BlockHashToBlockKey(blockHash)
	data, err := desoBlock.ToBytes(false)
	if err != nil {
		return err
	}
	// First check to see if the block is already in the db.
	if _, err := DBGetWithTxn(txn, snap, blockKey); err == nil {
		// err == nil means the block already exists in the db so
		// no need to store it.
		return nil
	}
	// If the block is not in the db then set it.
	if err := DBSetWithTxn(txn, snap, blockKey, data); err != nil {
		return err
	}

	// Index the block reward. Used for deducting immature block rewards from user balances.
	if len(desoBlock.Txns) == 0 {
		return fmt.Errorf("PutBlockWithTxn: Got block without any txns %v", desoBlock)
	}
	blockRewardTxn := desoBlock.Txns[0]
	if blockRewardTxn.TxnMeta.GetTxnType() != TxnTypeBlockReward {
		return fmt.Errorf("PutBlockWithTxn: Got block without block reward as first txn %v", desoBlock)
	}
	// It's possible the block reward is split across multiple public keys.
	pubKeyToBlockRewardMap := make(map[PkMapKey]uint64)
	for _, bro := range desoBlock.Txns[0].TxOutputs {
		pkMapKey := MakePkMapKey(bro.PublicKey)
		if _, hasKey := pubKeyToBlockRewardMap[pkMapKey]; !hasKey {
			pubKeyToBlockRewardMap[pkMapKey] = bro.AmountNanos
		} else {
			pubKeyToBlockRewardMap[pkMapKey] += bro.AmountNanos
		}
	}
	for pkMapKey, blockReward := range pubKeyToBlockRewardMap {
		blockRewardKey := PublicKeyBlockHashToBlockRewardKey(pkMapKey[:], blockHash)
		if err := DBSetWithTxn(txn, snap, blockRewardKey, EncodeUint64(blockReward)); err != nil {
			return err
		}
	}

	return nil
}

func PutBlock(handle *badger.DB, snap *Snapshot, desoBlock *MsgDeSoBlock) error {
	err := handle.Update(func(txn *badger.Txn) error {
		return PutBlockWithTxn(txn, snap, desoBlock)
	})
	if err != nil {
		return err
	}

	return nil
}

func DbGetBlockRewardForPublicKeyBlockHashWithTxn(txn *badger.Txn, snap *Snapshot, publicKey []byte, blockHash *BlockHash,
) (_balance uint64, _err error) {
	key := PublicKeyBlockHashToBlockRewardKey(publicKey, blockHash)
	desoBalanceBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return uint64(0), nil
	}
	return DecodeUint64(desoBalanceBytes), nil
}

func DbGetBlockRewardForPublicKeyBlockHash(db *badger.DB, snap *Snapshot, publicKey []byte, blockHash *BlockHash,
) (_balance uint64, _err error) {
	ret := uint64(0)
	dbErr := db.View(func(txn *badger.Txn) error {
		var err error
		ret, err = DbGetBlockRewardForPublicKeyBlockHashWithTxn(txn, snap, publicKey, blockHash)
		if err != nil {
			return errors.Wrap(err, "DbGetBlockRewardForPublicKeyBlockHash: ")
		}
		return nil
	})
	if dbErr != nil {
		return uint64(0), dbErr
	}
	return ret, nil
}

func _heightHashToNodeIndexPrefix(bitcoinNodes bool) []byte {
	prefix := append([]byte{}, _PrefixHeightHashToNodeInfo...)
	if bitcoinNodes {
		prefix = append([]byte{}, _PrefixBitcoinHeightHashToNodeInfo...)
	}

	return prefix
}

func _heightHashToNodeIndexKey(height uint32, hash *BlockHash, bitcoinNodes bool) []byte {
	prefix := _heightHashToNodeIndexPrefix(bitcoinNodes)

	heightBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(heightBytes[:], height)
	key := append(prefix, heightBytes[:]...)
	key = append(key, hash[:]...)

	return key
}

func GetHeightHashToNodeInfoWithTxn(txn *badger.Txn, snap *Snapshot,
	height uint32, hash *BlockHash, bitcoinNodes bool) *BlockNode {

	key := _heightHashToNodeIndexKey(height, hash, bitcoinNodes)
	nodeBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	var blockNode *BlockNode
	blockNode, err = DeserializeBlockNode(nodeBytes)
	if err != nil {
		return nil
	}
	return blockNode
}

func GetHeightHashToNodeInfo(handle *badger.DB, snap *Snapshot,
	height uint32, hash *BlockHash, bitcoinNodes bool) *BlockNode {

	var blockNode *BlockNode
	handle.View(func(txn *badger.Txn) error {
		blockNode = GetHeightHashToNodeInfoWithTxn(txn, snap, height, hash, bitcoinNodes)
		return nil
	})
	return blockNode
}

func PutHeightHashToNodeInfoWithTxn(txn *badger.Txn, snap *Snapshot,
	node *BlockNode, bitcoinNodes bool) error {

	key := _heightHashToNodeIndexKey(node.Height, node.Hash, bitcoinNodes)
	serializedNode, err := SerializeBlockNode(node)
	if err != nil {
		return errors.Wrapf(err, "PutHeightHashToNodeInfoWithTxn: Problem serializing node")
	}

	if err := DBSetWithTxn(txn, snap, key, serializedNode); err != nil {
		return err
	}
	return nil
}

func PutHeightHashToNodeInfo(handle *badger.DB, snap *Snapshot, node *BlockNode, bitcoinNodes bool) error {
	err := handle.Update(func(txn *badger.Txn) error {
		return PutHeightHashToNodeInfoWithTxn(txn, snap, node, bitcoinNodes)
	})

	if err != nil {
		return err
	}

	return nil
}

func DbDeleteHeightHashToNodeInfoWithTxn(txn *badger.Txn, snap *Snapshot,
	node *BlockNode, bitcoinNodes bool) error {

	return DBDeleteWithTxn(txn, snap, _heightHashToNodeIndexKey(node.Height, node.Hash, bitcoinNodes))
}

func DbBulkDeleteHeightHashToNodeInfo(handle *badger.DB, snap *Snapshot,
	nodes []*BlockNode, bitcoinNodes bool) error {

	err := handle.Update(func(txn *badger.Txn) error {
		for _, nn := range nodes {
			if err := DbDeleteHeightHashToNodeInfoWithTxn(txn, snap, nn, bitcoinNodes); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	return nil
}

// InitDbWithGenesisBlock initializes the database to contain only the genesis
// block.
func InitDbWithDeSoGenesisBlock(params *DeSoParams, handle *badger.DB,
	eventManager *EventManager, snap *Snapshot) error {
	// Construct a node for the genesis block. Its height is zero and it has
	// no parents. Its difficulty should be set to the initial
	// difficulty specified in the parameters and it should be assumed to be
	// valid and stored by the end of this function.
	genesisBlock := params.GenesisBlock
	diffTarget := MustDecodeHexBlockHash(params.MinDifficultyTargetHex)
	blockHash := MustDecodeHexBlockHash(params.GenesisBlockHashHex)
	genesisNode := NewBlockNode(
		nil, // Parent
		blockHash,
		0, // Height
		diffTarget,
		BytesToBigint(ExpectedWorkForBlockHash(diffTarget)[:]), // CumWork
		genesisBlock.Header, // Header
		StatusHeaderValidated|StatusBlockProcessed|StatusBlockStored|StatusBlockValidated, // Status
	)

	// Set the fields in the db to reflect the current state of our chain.
	//
	// Set the best hash to the genesis block in the db since its the only node
	// we're currently aware of. Set it for both the header chain and the block
	// chain.
	if err := PutBestHash(handle, snap, blockHash, ChainTypeDeSoBlock); err != nil {
		return errors.Wrapf(err, "InitDbWithGenesisBlock: Problem putting genesis block hash into db for block chain")
	}
	// Add the genesis block to the (hash -> block) index.
	if err := PutBlock(handle, snap, genesisBlock); err != nil {
		return errors.Wrapf(err, "InitDbWithGenesisBlock: Problem putting genesis block into db")
	}
	// Add the genesis block to the (height, hash -> node info) index in the db.
	if err := PutHeightHashToNodeInfo(handle, snap, genesisNode, false /*bitcoinNodes*/); err != nil {
		return errors.Wrapf(err, "InitDbWithGenesisBlock: Problem putting (height, hash -> node) in db")
	}
	if err := DbPutNanosPurchased(handle, snap, params.DeSoNanosPurchasedAtGenesis); err != nil {
		return errors.Wrapf(err, "InitDbWithGenesisBlock: Problem putting genesis block hash into db for block chain")
	}
	if err := DbPutGlobalParamsEntry(handle, snap, InitialGlobalParamsEntry); err != nil {
		return errors.Wrapf(err, "InitDbWithGenesisBlock: Problem putting GlobalParamsEntry into db for block chain")
	}

	// We apply seed transactions here. This step is useful for setting
	// up the blockchain with a particular set of transactions, e.g. when
	// hard forking the chain.
	//
	// TODO: Right now there's an issue where if we hit an error during this
	// step of the initialization, the next time we run the program it will
	// think things are initialized because we set the best block hash at the
	// top. We should fix this at some point so that an error in this step
	// wipes out the best hash.
	utxoView, err := NewUtxoView(handle, params, nil, snap)
	if err != nil {
		return fmt.Errorf(
			"InitDbWithDeSoGenesisBlock: Error initializing UtxoView")
	}

	// Add the seed balances to the view.
	for index, txOutput := range params.SeedBalances {
		outputKey := UtxoKey{
			TxID:  BlockHash{},
			Index: uint32(index),
		}
		utxoEntry := UtxoEntry{
			AmountNanos: txOutput.AmountNanos,
			PublicKey:   txOutput.PublicKey,
			BlockHeight: 0,
			// Just make this a normal transaction so that we don't have to wait for
			// the block reward maturity.
			UtxoType: UtxoTypeOutput,
			UtxoKey:  &outputKey,
		}

		_, err := utxoView._addUtxo(&utxoEntry)
		if err != nil {
			return fmt.Errorf("InitDbWithDeSoGenesisBlock: Error adding "+
				"seed balance at index %v ; output: %v: %v", index, txOutput, err)
		}
	}

	// Add the seed txns to the view
	utxoOpsForBlock := [][]*UtxoOperation{}
	for txnIndex, txnHex := range params.SeedTxns {
		txnBytes, err := hex.DecodeString(txnHex)
		if err != nil {
			return fmt.Errorf(
				"InitDbWithDeSoGenesisBlock: Error decoding seed "+
					"txn HEX: %v, txn index: %v, txn hex: %v",
				err, txnIndex, txnHex)
		}
		txn := &MsgDeSoTxn{}
		if err := txn.FromBytes(txnBytes); err != nil {
			return fmt.Errorf(
				"InitDbWithDeSoGenesisBlock: Error decoding seed "+
					"txn BYTES: %v, txn index: %v, txn hex: %v",
				err, txnIndex, txnHex)
		}
		// Important: ignoreUtxos makes it so that the inputs/outputs aren't
		// processed, which is important.
		// Set txnSizeBytes to 0 here as the minimum network fee is 0 at genesis block, so there is no need to serialize
		// these transactions to check if they meet the minimum network fee requirement.
		var utxoOpsForTxn []*UtxoOperation
		utxoOpsForTxn, _, _, _, err = utxoView.ConnectTransaction(
			txn, txn.Hash(), 0, 0 /*blockHeight*/, false /*verifySignatures*/, true /*ignoreUtxos*/)
		if err != nil {
			return fmt.Errorf(
				"InitDbWithDeSoGenesisBlock: Error connecting transaction: %v, "+
					"txn index: %v, txn hex: %v",
				err, txnIndex, txnHex)
		}
		utxoOpsForBlock = append(utxoOpsForBlock, utxoOpsForTxn)
	}

	// If we have an event manager, initialize the genesis block with the current
	// state of the view.
	if eventManager != nil {
		eventManager.blockConnected(&BlockEvent{
			Block:    genesisBlock,
			UtxoView: utxoView,
			UtxoOps:  utxoOpsForBlock,
		})
	}

	// Flush all the data in the view.
	err = utxoView.FlushToDb()
	if err != nil {
		return fmt.Errorf(
			"InitDbWithDeSoGenesisBlock: Error flushing seed txns to DB: %v", err)
	}

	return nil
}

func GetBlockIndex(handle *badger.DB, bitcoinNodes bool) (map[BlockHash]*BlockNode, error) {
	blockIndex := make(map[BlockHash]*BlockNode)

	prefix := _heightHashToNodeIndexPrefix(bitcoinNodes)

	err := handle.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		nodeIterator := txn.NewIterator(opts)
		defer nodeIterator.Close()
		for nodeIterator.Seek(prefix); nodeIterator.ValidForPrefix(prefix); nodeIterator.Next() {
			var blockNode *BlockNode

			// Don't bother checking the key. We assume that the key lines up
			// with what we've stored in the value in terms of (height, block hash).
			item := nodeIterator.Item()
			err := item.Value(func(blockNodeBytes []byte) error {
				// Deserialize the block node.
				var err error
				// TODO: There is room for optimization here by pre-allocating a
				// contiguous list of block nodes and then populating that list
				// rather than having each blockNode be a stand-alone allocation.
				blockNode, err = DeserializeBlockNode(blockNodeBytes)
				if err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				return err
			}

			// If we got here it means we read a blockNode successfully. Store it
			// into our node index.
			blockIndex[*blockNode.Hash] = blockNode

			// Find the parent of this block, which should already have been read
			// in and connect it. Skip the genesis block, which has height 0. Also
			// skip the block if its PrevBlockHash is empty, which will be true for
			// the BitcoinStartBlockNode.
			//
			// TODO: There is room for optimization here by keeping a reference to
			// the last node we've iterated over and checking if that node is the
			// parent. Doing this would avoid an expensive hashmap check to get
			// the parent by its block hash.
			if blockNode.Height == 0 || (*blockNode.Header.PrevBlockHash == BlockHash{}) {
				continue
			}
			if parent, ok := blockIndex[*blockNode.Header.PrevBlockHash]; ok {
				// We found the parent node so connect it.
				blockNode.Parent = parent
			} else {
				// In this case we didn't find the parent so error. There shouldn't
				// be any unconnectedTxns in our block index.
				return fmt.Errorf("GetBlockIndex: Could not find parent for blockNode: %+v", blockNode)
			}
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "GetBlockIndex: Problem reading block index from db")
	}

	return blockIndex, nil
}

func GetBestChain(tipNode *BlockNode, blockIndex map[BlockHash]*BlockNode) ([]*BlockNode, error) {
	reversedBestChain := []*BlockNode{}
	for tipNode != nil {
		if (tipNode.Status&StatusBlockValidated) == 0 &&
			(tipNode.Status&StatusBitcoinHeaderValidated) == 0 {

			return nil, fmt.Errorf("GetBestChain: Invalid node found in main chain: %+v", tipNode)
		}

		reversedBestChain = append(reversedBestChain, tipNode)
		tipNode = tipNode.Parent
	}

	bestChain := make([]*BlockNode, len(reversedBestChain))
	for ii := 0; ii < len(reversedBestChain); ii++ {
		bestChain[ii] = reversedBestChain[len(reversedBestChain)-1-ii]
	}

	return bestChain, nil
}

// RandomBytes returns a []byte with random values.
func RandomBytes(numBytes int32) []byte {
	randomBytes := make([]byte, numBytes)
	_, err := rand.Read(randomBytes)
	if err != nil {
		glog.Errorf("Problem reading random bytes: %v", err)
	}
	return randomBytes
}

// RandomBytesHex returns a hex string representing numBytes of
// entropy.
func RandomBytesHex(numBytes int32) string {
	return hex.EncodeToString(RandomBytes(numBytes))
}

// RandInt64 returns a random 64-bit int.
func RandInt64(max int64) int64 {
	val, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		glog.Errorf("Problem generating random int64: %v", err)
	}
	return val.Int64()
}

// RandInt32 returns a random 32-bit int.
func RandInt32(max int32) int32 {
	val, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt32))
	if err != nil {
		glog.Errorf("Problem generating random int32: %v", err)
	}
	if val.Int64() > math.MaxInt32 {
		glog.Errorf("Generated a random number out of range: %d (max: %d)", val.Int64(), math.MaxInt32)
	}
	// This cast is OK since we initialized the number to be
	// < MaxInt32 above.
	return int32(val.Int64())
}

// PPrintJSON prints a JSON object but pretty.
func PPrintJSON(xx interface{}) {
	yy, _ := json.MarshalIndent(xx, "", "  ")
	log.Println(string(yy))
}

func BlocksPerDuration(duration time.Duration, timeBetweenBlocks time.Duration) uint32 {
	return uint32(int64(duration) / int64(timeBetweenBlocks))
}

func PkToString(pk []byte, params *DeSoParams) string {
	return Base58CheckEncode(pk, false, params)
}

func PrivToString(priv []byte, params *DeSoParams) string {
	return Base58CheckEncode(priv, true, params)
}

func PkToStringMainnet(pk []byte) string {
	return Base58CheckEncode(pk, false, &DeSoMainnetParams)
}

func PkToStringBoth(pk []byte) string {
	return PkToStringMainnet(pk) + ":" + PkToStringTestnet(pk)
}

func PkToStringTestnet(pk []byte) string {
	return Base58CheckEncode(pk, false, &DeSoTestnetParams)
}

func DbGetTxindexTip(handle *badger.DB, snap *Snapshot) *BlockHash {
	return _getBlockHashForPrefix(handle, snap, _KeyTransactionIndexTip)
}

func DbPutTxindexTipWithTxn(txn *badger.Txn, snap *Snapshot, tipHash *BlockHash) error {
	return DBSetWithTxn(txn, snap, _KeyTransactionIndexTip, tipHash[:])
}

func DbPutTxindexTip(handle *badger.DB, snap *Snapshot, tipHash *BlockHash) error {
	return handle.Update(func(txn *badger.Txn) error {
		return DbPutTxindexTipWithTxn(txn, snap, tipHash)
	})
}

func _DbTxindexPublicKeyNextIndexPrefix(publicKey []byte) []byte {
	return append(append([]byte{}, _PrefixPublicKeyToNextIndex...), publicKey...)
}

func DbTxindexPublicKeyPrefix(publicKey []byte) []byte {
	return append(append([]byte{}, _PrefixPublicKeyIndexToTransactionIDs...), publicKey...)
}

func DbTxindexPublicKeyIndexToTxnKey(publicKey []byte, index uint32) []byte {
	prefix := DbTxindexPublicKeyPrefix(publicKey)
	return append(prefix, _EncodeUint32(index)...)
}

func DbGetTxindexTxnsForPublicKeyWithTxn(txn *badger.Txn, publicKey []byte) []*BlockHash {
	txIDs := []*BlockHash{}
	_, valsFound, err := _enumerateKeysForPrefixWithTxn(txn, DbTxindexPublicKeyPrefix(publicKey))
	if err != nil {
		return txIDs
	}
	for _, txIDBytes := range valsFound {
		blockHash := &BlockHash{}
		copy(blockHash[:], txIDBytes[:])
		txIDs = append(txIDs, blockHash)
	}

	return txIDs
}

func DbGetTxindexTxnsForPublicKey(handle *badger.DB, publicKey []byte) []*BlockHash {
	txIDs := []*BlockHash{}
	handle.Update(func(txn *badger.Txn) error {
		txIDs = DbGetTxindexTxnsForPublicKeyWithTxn(txn, publicKey)
		return nil
	})
	return txIDs
}

func _DbGetTxindexNextIndexForPublicKeBySeekWithTxn(txn *badger.Txn, publicKey []byte) uint64 {
	dbPrefixx := DbTxindexPublicKeyPrefix(publicKey)

	opts := badger.DefaultIteratorOptions

	opts.PrefetchValues = false

	// Go in reverse order.
	opts.Reverse = true

	it := txn.NewIterator(opts)
	defer it.Close()
	// Since we iterate backwards, the prefix must be bigger than all possible
	// counts that could actually exist. We use four bytes since the index is
	// encoded as a 32-bit big-endian byte slice, which will be four bytes long.
	maxBigEndianUint32Bytes := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	prefix := append([]byte{}, dbPrefixx...)
	prefix = append(prefix, maxBigEndianUint32Bytes...)
	for it.Seek(prefix); it.ValidForPrefix(dbPrefixx); it.Next() {
		countKey := it.Item().Key()

		// Strip the prefix off the key and check its length. If it contains
		// a big-endian uint32 then it should be at least four bytes.
		countKey = countKey[len(dbPrefixx):]
		if len(countKey) < len(maxBigEndianUint32Bytes) {
			glog.Errorf("DbGetTxindexNextIndexForPublicKey: Invalid public key "+
				"index key length %d should be at least %d",
				len(countKey), len(maxBigEndianUint32Bytes))
			return 0
		}

		countVal := DecodeUint32(countKey[:len(maxBigEndianUint32Bytes)])
		return uint64(countVal + 1)
	}
	// If we get here it means we didn't find anything in the db so return zero.
	return 0
}

func DbGetTxindexNextIndexForPublicKey(handle *badger.DB, snap *Snapshot, publicKey []byte) *uint64 {
	var nextIndex *uint64
	handle.View(func(txn *badger.Txn) error {
		nextIndex = _DbGetTxindexNextIndexForPublicKeyWithTxn(txn, snap, publicKey)
		return nil
	})
	return nextIndex
}

func _DbGetTxindexNextIndexForPublicKeyWithTxn(txn *badger.Txn, snap *Snapshot, publicKey []byte) *uint64 {
	key := _DbTxindexPublicKeyNextIndexPrefix(publicKey)
	valBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		// If we haven't seen this public key yet, we won't have a next index for this key yet, so return 0.
		if errors.Is(err, badger.ErrKeyNotFound) {
			nextIndexVal := _DbGetTxindexNextIndexForPublicKeBySeekWithTxn(txn, publicKey)
			return &nextIndexVal
		} else {
			return nil
		}
	}
	nextIndexVal, bytesRead := Uvarint(valBytes)
	if bytesRead <= 0 {
		return nil
	}
	return &nextIndexVal

}

func DbPutTxindexNextIndexForPublicKeyWithTxn(txn *badger.Txn, snap *Snapshot,
	publicKey []byte, nextIndex uint64) error {

	key := _DbTxindexPublicKeyNextIndexPrefix(publicKey)
	valBuf := UintToBuf(nextIndex)

	return DBSetWithTxn(txn, snap, key, valBuf)
}

func DbDeleteTxindexNextIndexForPublicKeyWithTxn(txn *badger.Txn, snap *Snapshot, publicKey []byte) error {
	key := _DbTxindexPublicKeyNextIndexPrefix(publicKey)
	return DBDeleteWithTxn(txn, snap, key)
}

func DbPutTxindexPublicKeyToTxnMappingSingleWithTxn(txn *badger.Txn, snap *Snapshot,
	publicKey []byte, txID *BlockHash) error {

	nextIndex := _DbGetTxindexNextIndexForPublicKeyWithTxn(txn, snap, publicKey)
	if nextIndex == nil {
		return fmt.Errorf("Error getting next index")
	}
	key := DbTxindexPublicKeyIndexToTxnKey(publicKey, uint32(*nextIndex))
	err := DbPutTxindexNextIndexForPublicKeyWithTxn(txn, snap, publicKey, uint64(*nextIndex+1))
	if err != nil {
		return err
	}
	return DBSetWithTxn(txn, snap, key, txID[:])
}

func DbDeleteTxindexPublicKeyToTxnMappingSingleWithTxn(txn *badger.Txn,
	snap *Snapshot, publicKey []byte, txID *BlockHash) error {

	// Get all the mappings corresponding to the public key passed in.
	// TODO: This is inefficient but reorgs are rare so whatever.
	txIDsInDB := DbGetTxindexTxnsForPublicKeyWithTxn(txn, publicKey)
	numMappingsInDB := len(txIDsInDB)

	// Loop over the list of txIDs and delete the one
	// corresponding to the passed-in transaction. Note we can assume that
	// only one occurrence exists in the list.
	// TODO: Looping backwards would be more efficient.
	for ii, singleTxID := range txIDsInDB {
		if *singleTxID == *txID {
			// If we get here it means the transaction we need to delete is at
			// this index.
			txIDsInDB = append(txIDsInDB[:ii], txIDsInDB[ii+1:]...)
			break
		}
	}

	// Delete all the mappings from the db.
	for pkIndex := 0; pkIndex < numMappingsInDB; pkIndex++ {
		key := DbTxindexPublicKeyIndexToTxnKey(publicKey, uint32(pkIndex))
		if err := DBDeleteWithTxn(txn, snap, key); err != nil {
			return err
		}
	}

	// Delete the next index for this public key
	err := DbDeleteTxindexNextIndexForPublicKeyWithTxn(txn, snap, publicKey)
	if err != nil {
		return err
	}

	// Re-add all the mappings to the db except the one we just deleted.
	for _, singleTxID := range txIDsInDB {
		if err := DbPutTxindexPublicKeyToTxnMappingSingleWithTxn(txn, snap, publicKey, singleTxID); err != nil {
			return err
		}
	}

	// At this point the db should contain all transactions except the one
	// that was deleted.
	return nil
}

func DbTxindexTxIDKey(txID *BlockHash) []byte {
	return append(append([]byte{}, _PrefixTransactionIDToMetadata...), txID[:]...)
}

type AffectedPublicKey struct {
	PublicKeyBase58Check string
	// Metadata about how this public key was affected by the transaction.
	Metadata string
}

type BasicTransferTxindexMetadata struct {
	TotalInputNanos  uint64
	TotalOutputNanos uint64
	FeeNanos         uint64
	UtxoOpsDump      string
	UtxoOps          []*UtxoOperation
	DiamondLevel     int64
	PostHashHex      string
}
type BitcoinExchangeTxindexMetadata struct {
	BitcoinSpendAddress string
	// DeSoOutputPubKeyBase58Check = TransactorPublicKeyBase58Check
	SatoshisBurned uint64
	// NanosCreated = 0 OR TotalOutputNanos+FeeNanos
	NanosCreated uint64
	// TotalNanosPurchasedBefore = TotalNanosPurchasedAfter - NanosCreated
	TotalNanosPurchasedBefore uint64
	TotalNanosPurchasedAfter  uint64
	BitcoinTxnHash            string
}
type CreatorCoinTxindexMetadata struct {
	OperationType string
	// TransactorPublicKeyBase58Check = TransactorPublicKeyBase58Check
	// CreatorPublicKeyBase58Check in AffectedPublicKeys

	// Differs depending on OperationType.
	DeSoToSellNanos        uint64
	CreatorCoinToSellNanos uint64
	DeSoToAddNanos         uint64

	// Rosetta needs to know how much DESO was added or removed so it can
	// model the change to the total deso locked in the creator coin
	DESOLockedNanosDiff int64
}

type CreatorCoinTransferTxindexMetadata struct {
	CreatorUsername            string
	CreatorCoinToTransferNanos uint64
	DiamondLevel               int64
	PostHashHex                string
}

type UpdateProfileTxindexMetadata struct {
	ProfilePublicKeyBase58Check string

	NewUsername    string
	NewDescription string
	NewProfilePic  string

	NewCreatorBasisPoints uint64

	NewStakeMultipleBasisPoints uint64

	IsHidden bool
}
type SubmitPostTxindexMetadata struct {
	PostHashBeingModifiedHex string
	// PosterPublicKeyBase58Check = TransactorPublicKeyBase58Check

	// If this is a reply to an existing post, then the ParentPostHashHex
	ParentPostHashHex string
	// ParentPosterPublicKeyBase58Check in AffectedPublicKeys

	// The profiles that are mentioned are in the AffectedPublicKeys
	// MentionedPublicKeyBase58Check in AffectedPublicKeys
}
type LikeTxindexMetadata struct {
	// LikerPublicKeyBase58Check = TransactorPublicKeyBase58Check
	IsUnlike bool

	PostHashHex string
	// PosterPublicKeyBase58Check in AffectedPublicKeys
}
type FollowTxindexMetadata struct {
	// FollowerPublicKeyBase58Check = TransactorPublicKeyBase58Check
	// FollowedPublicKeyBase58Check in AffectedPublicKeys

	IsUnfollow bool
}
type PrivateMessageTxindexMetadata struct {
	// SenderPublicKeyBase58Check = TransactorPublicKeyBase58Check
	// RecipientPublicKeyBase58Check in AffectedPublicKeys

	TimestampNanos uint64
}
type SwapIdentityTxindexMetadata struct {
	// ParamUpdater = TransactorPublicKeyBase58Check

	FromPublicKeyBase58Check string
	ToPublicKeyBase58Check   string

	// Rosetta needs this information to track creator coin balances
	FromDeSoLockedNanos uint64
	ToDeSoLockedNanos   uint64
}

type NFTBidTxindexMetadata struct {
	NFTPostHashHex string
	SerialNumber   uint64
	BidAmountNanos uint64
}

type AcceptNFTBidTxindexMetadata struct {
	NFTPostHashHex              string
	SerialNumber                uint64
	BidAmountNanos              uint64
	CreatorCoinRoyaltyNanos     uint64
	CreatorPublicKeyBase58Check string
}

type NFTTransferTxindexMetadata struct {
	NFTPostHashHex string
	SerialNumber   uint64
}

type TransactionMetadata struct {
	BlockHashHex    string
	TxnIndexInBlock uint64
	TxnType         string
	// All transactions have a public key who executed the transaction and some
	// public keys that are affected by the transaction. Notifications are created
	// for the affected public keys. _getPublicKeysForTxn uses this to set entries in the
	// database.
	TransactorPublicKeyBase58Check string
	AffectedPublicKeys             []*AffectedPublicKey

	// We store these outputs so we don't have to load the full transaction from disk
	// when looking up output amounts
	TxnOutputs []*DeSoOutput

	BasicTransferTxindexMetadata       *BasicTransferTxindexMetadata       `json:",omitempty"`
	BitcoinExchangeTxindexMetadata     *BitcoinExchangeTxindexMetadata     `json:",omitempty"`
	CreatorCoinTxindexMetadata         *CreatorCoinTxindexMetadata         `json:",omitempty"`
	CreatorCoinTransferTxindexMetadata *CreatorCoinTransferTxindexMetadata `json:",omitempty"`
	UpdateProfileTxindexMetadata       *UpdateProfileTxindexMetadata       `json:",omitempty"`
	SubmitPostTxindexMetadata          *SubmitPostTxindexMetadata          `json:",omitempty"`
	LikeTxindexMetadata                *LikeTxindexMetadata                `json:",omitempty"`
	FollowTxindexMetadata              *FollowTxindexMetadata              `json:",omitempty"`
	PrivateMessageTxindexMetadata      *PrivateMessageTxindexMetadata      `json:",omitempty"`
	SwapIdentityTxindexMetadata        *SwapIdentityTxindexMetadata        `json:",omitempty"`
	NFTBidTxindexMetadata              *NFTBidTxindexMetadata              `json:",omitempty"`
	AcceptNFTBidTxindexMetadata        *AcceptNFTBidTxindexMetadata        `json:",omitempty"`
	NFTTransferTxindexMetadata         *NFTTransferTxindexMetadata         `json:",omitempty"`
}

func DBCheckTxnExistenceWithTxn(txn *badger.Txn, snap *Snapshot, txID *BlockHash) bool {
	key := DbTxindexTxIDKey(txID)
	_, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return false
	}
	return true
}

func DbCheckTxnExistence(handle *badger.DB, snap *Snapshot, txID *BlockHash) bool {
	var exists bool
	handle.View(func(txn *badger.Txn) error {
		exists = DBCheckTxnExistenceWithTxn(txn, snap, txID)
		return nil
	})
	return exists
}

func DbGetTxindexTransactionRefByTxIDWithTxn(txn *badger.Txn, snap *Snapshot, txID *BlockHash) *TransactionMetadata {
	key := DbTxindexTxIDKey(txID)
	valObj := TransactionMetadata{}

	valBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}
	if err := gob.NewDecoder(bytes.NewReader(valBytes)).Decode(&valObj); err != nil {
		return nil
	}
	return &valObj
}

func DbGetTxindexTransactionRefByTxID(handle *badger.DB, snap *Snapshot, txID *BlockHash) *TransactionMetadata {
	var valObj *TransactionMetadata
	handle.View(func(txn *badger.Txn) error {
		valObj = DbGetTxindexTransactionRefByTxIDWithTxn(txn, snap, txID)
		return nil
	})
	return valObj
}
func DbPutTxindexTransactionWithTxn(txn *badger.Txn, snap *Snapshot,
	txID *BlockHash, txnMeta *TransactionMetadata) error {

	key := append(append([]byte{}, _PrefixTransactionIDToMetadata...), txID[:]...)
	valBuf := bytes.NewBuffer([]byte{})
	gob.NewEncoder(valBuf).Encode(txnMeta)

	return DBSetWithTxn(txn, snap, key, valBuf.Bytes())
}

func DbPutTxindexTransaction(handle *badger.DB, snap *Snapshot,
	txID *BlockHash, txnMeta *TransactionMetadata) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutTxindexTransactionWithTxn(txn, snap, txID, txnMeta)
	})
}

func _getPublicKeysForTxn(
	txn *MsgDeSoTxn, txnMeta *TransactionMetadata, params *DeSoParams) map[PkMapKey]bool {

	// Collect the public keys in the transaction.
	publicKeys := make(map[PkMapKey]bool)

	// TODO: For AddStake transactions, we don't have a way of getting the implicit
	// outputs. This means that if you get paid from someone else staking to a post
	// after you, the output won't be explicitly included in the transaction, and so
	// it won't be added to our index. We should fix this at some point. I think the
	// "right way" to fix this problem is to index UTXOs rather than transactions (or
	// in addition to them).
	// TODO(updated): We can fix this by populating AffectedPublicKeys

	// Add the TransactorPublicKey
	{
		res, _, err := Base58CheckDecode(txnMeta.TransactorPublicKeyBase58Check)
		if err != nil {
			glog.Errorf("_getPublicKeysForTxn: Error decoding "+
				"TransactorPublicKeyBase58Check: %v %v",
				txnMeta.TransactorPublicKeyBase58Check, err)
		} else {
			publicKeys[MakePkMapKey(res)] = true
		}
	}

	// Add each AffectedPublicKey
	for _, affectedPk := range txnMeta.AffectedPublicKeys {
		res, _, err := Base58CheckDecode(affectedPk.PublicKeyBase58Check)
		if err != nil {
			glog.Errorf("_getPublicKeysForTxn: Error decoding AffectedPublicKey: %v %v %v",
				affectedPk.PublicKeyBase58Check, affectedPk.Metadata, err)
		} else {
			publicKeys[MakePkMapKey(res)] = true
		}
	}

	return publicKeys
}

func DbPutTxindexTransactionMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	desoTxn *MsgDeSoTxn, params *DeSoParams, txnMeta *TransactionMetadata) error {

	txID := desoTxn.Hash()

	if err := DbPutTxindexTransactionWithTxn(txn, snap, txID, txnMeta); err != nil {
		return fmt.Errorf("Problem adding txn to txindex transaction index: %v", err)
	}

	// Get the public keys involved with this transaction.
	publicKeys := _getPublicKeysForTxn(desoTxn, txnMeta, params)

	// For each public key found, add the txID from its list.
	for pkFound := range publicKeys {
		// Simply add a new entry for each of the public keys found.
		if err := DbPutTxindexPublicKeyToTxnMappingSingleWithTxn(txn, snap, pkFound[:], txID); err != nil {
			return err
		}
	}

	// If we get here, it means everything went smoothly.
	return nil
}

func DbPutTxindexTransactionMappings(handle *badger.DB, snap *Snapshot,
	desoTxn *MsgDeSoTxn, params *DeSoParams, txnMeta *TransactionMetadata) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutTxindexTransactionMappingsWithTxn(
			txn, snap, desoTxn, params, txnMeta)
	})
}

func DbDeleteTxindexTransactionMappingsWithTxn(txn *badger.Txn,
	snap *Snapshot, desoTxn *MsgDeSoTxn, params *DeSoParams) error {

	txID := desoTxn.Hash()

	// If the txnMeta isn't in the db then that's an error.
	txnMeta := DbGetTxindexTransactionRefByTxIDWithTxn(txn, snap, txID)
	if txnMeta == nil {
		return fmt.Errorf("DbDeleteTxindexTransactionMappingsWithTxn: Missing txnMeta for txID %v", txID)
	}

	// Get the public keys involved with this transaction.
	publicKeys := _getPublicKeysForTxn(desoTxn, txnMeta, params)

	// For each public key found, delete the txID mapping from the db.
	for pkFound := range publicKeys {
		if err := DbDeleteTxindexPublicKeyToTxnMappingSingleWithTxn(txn, snap, pkFound[:], txID); err != nil {
			return err
		}
	}

	// Delete the metadata
	transactionIndexKey := DbTxindexTxIDKey(txID)
	if err := DBDeleteWithTxn(txn, snap, transactionIndexKey); err != nil {
		return fmt.Errorf("Problem deleting transaction index key: %v", err)
	}

	// If we get here, it means everything went smoothly.
	return nil
}

func DbDeleteTxindexTransactionMappings(handle *badger.DB, snap *Snapshot,
	desoTxn *MsgDeSoTxn, params *DeSoParams) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbDeleteTxindexTransactionMappingsWithTxn(txn, snap, desoTxn, params)
	})
}

// DbGetTxindexFullTransactionByTxID
// TODO: This makes lookups inefficient when blocks are large. Shouldn't be a
// problem for a while, but keep an eye on it.
func DbGetTxindexFullTransactionByTxID(txindexDBHandle *badger.DB, snap *Snapshot,
	blockchainDBHandle *badger.DB, txID *BlockHash) (
	_txn *MsgDeSoTxn, _txnMeta *TransactionMetadata) {

	var txnFound *MsgDeSoTxn
	var txnMeta *TransactionMetadata
	err := txindexDBHandle.View(func(txn *badger.Txn) error {
		txnMeta = DbGetTxindexTransactionRefByTxIDWithTxn(txn, snap, txID)
		if txnMeta == nil {
			return fmt.Errorf("DbGetTxindexFullTransactionByTxID: Transaction not found")
		}
		blockHashBytes, err := hex.DecodeString(txnMeta.BlockHashHex)
		if err != nil {
			return fmt.Errorf("DbGetTxindexFullTransactionByTxID: Error parsing block "+
				"hash hex: %v %v", txnMeta.BlockHashHex, err)
		}
		blockHash := &BlockHash{}
		copy(blockHash[:], blockHashBytes)
		blockFound, err := GetBlock(blockHash, blockchainDBHandle, snap)
		if blockFound == nil || err != nil {
			return fmt.Errorf("DbGetTxindexFullTransactionByTxID: Block corresponding to txn not found")
		}

		txnFound = blockFound.Txns[txnMeta.TxnIndexInBlock]
		return nil
	})
	if err != nil {
		return nil, nil
	}

	return txnFound, txnMeta
}

// =======================================================================================
// DeSo app code start
// =======================================================================================

func _dbKeyForPostEntryHash(postHash *BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixPostHashToPostEntry...)
	key := append(prefixCopy, postHash[:]...)
	return key
}
func _dbKeyForPublicKeyPostHash(publicKey []byte, postHash *BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	key := append([]byte{}, _PrefixPosterPublicKeyPostHash...)
	key = append(key, publicKey...)
	key = append(key, postHash[:]...)
	return key
}
func _dbKeyForPosterPublicKeyTimestampPostHash(publicKey []byte, timestampNanos uint64, postHash *BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	key := append([]byte{}, _PrefixPosterPublicKeyTimestampPostHash...)
	key = append(key, publicKey...)
	key = append(key, EncodeUint64(timestampNanos)...)
	key = append(key, postHash[:]...)
	return key
}
func _dbKeyForTstampPostHash(tstampNanos uint64, postHash *BlockHash) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	key := append([]byte{}, _PrefixTstampNanosPostHash...)
	key = append(key, EncodeUint64(tstampNanos)...)
	key = append(key, postHash[:]...)
	return key
}
func _dbKeyForCreatorBpsPostHash(creatorBps uint64, postHash *BlockHash) []byte {
	key := append([]byte{}, _PrefixCreatorBpsPostHash...)
	key = append(key, EncodeUint64(creatorBps)...)
	key = append(key, postHash[:]...)
	return key
}
func _dbKeyForStakeMultipleBpsPostHash(stakeMultipleBps uint64, postHash *BlockHash) []byte {
	key := append([]byte{}, _PrefixMultipleBpsPostHash...)
	key = append(key, EncodeUint64(stakeMultipleBps)...)
	key = append(key, postHash[:]...)
	return key
}
func _dbKeyForCommentParentStakeIDToPostHash(
	stakeID []byte, tstampNanos uint64, postHash *BlockHash) []byte {
	key := append([]byte{}, _PrefixCommentParentStakeIDToPostHash...)
	key = append(key, stakeID[:]...)
	key = append(key, EncodeUint64(tstampNanos)...)
	key = append(key, postHash[:]...)
	return key
}

func DBGetPostEntryByPostHashWithTxn(txn *badger.Txn, snap *Snapshot,
	postHash *BlockHash) *PostEntry {

	key := _dbKeyForPostEntryHash(postHash)
	postEntryBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	postEntryObj := &PostEntry{}
	postEntryObj.Decode(postEntryBytes)
	return postEntryObj
}

func DBGetPostEntryByPostHash(db *badger.DB, snap *Snapshot, postHash *BlockHash) *PostEntry {
	var ret *PostEntry
	db.View(func(txn *badger.Txn) error {
		ret = DBGetPostEntryByPostHashWithTxn(txn, snap, postHash)
		return nil
	})
	return ret
}

func DBDeletePostEntryMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	postHash *BlockHash, params *DeSoParams) error {

	// First pull up the mapping that exists for the post hash passed in.
	// If one doesn't exist then there's nothing to do.
	postEntry := DBGetPostEntryByPostHashWithTxn(txn, snap, postHash)
	if postEntry == nil {
		return nil
	}

	// When a post exists, delete the mapping for the post.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForPostEntryHash(postHash)); err != nil {
		return errors.Wrapf(err, "DbDeletePostEntryMappingsWithTxn: Deleting "+
			"post mapping for post hash %v", postHash)
	}

	// If the post is a comment we store it in a separate index. Comments are
	// technically posts but they really should be treated as their own entity.
	// The only reason they're not actually implemented that way is so that we
	// get code re-use.
	isComment := len(postEntry.ParentStakeID) == HashSizeBytes
	if isComment {
		// Extend the parent stake ID, which is a block hash, to 33 bytes, which
		// is the length of a public key and the standard length we use for this
		// key.
		extendedStakeID := append([]byte{}, postEntry.ParentStakeID...)
		extendedStakeID = append(extendedStakeID, 0x00)
		parentStakeIDKey := _dbKeyForCommentParentStakeIDToPostHash(
			extendedStakeID, postEntry.TimestampNanos, postEntry.PostHash)
		if err := DBDeleteWithTxn(txn, snap, parentStakeIDKey); err != nil {

			return errors.Wrapf(err, "DbDeletePostEntryMappingsWithTxn: Problem "+
				"deleting mapping for comment: %v: %v", postEntry, err)
		}
	} else {
		if err := DBDeleteWithTxn(txn, snap, _dbKeyForPosterPublicKeyTimestampPostHash(
			postEntry.PosterPublicKey, postEntry.TimestampNanos, postEntry.PostHash)); err != nil {

			return errors.Wrapf(err, "DbDeletePostEntryMappingsWithTxn: Deleting "+
				"public key mapping for post hash %v: %v", postHash, err)
		}
		if err := DBDeleteWithTxn(txn, snap, _dbKeyForTstampPostHash(
			postEntry.TimestampNanos, postEntry.PostHash)); err != nil {

			return errors.Wrapf(err, "DbDeletePostEntryMappingsWithTxn: Deleting "+
				"tstamp mapping for post hash %v: %v", postHash, err)
		}
		if err := DBDeleteWithTxn(txn, snap, _dbKeyForCreatorBpsPostHash(
			postEntry.CreatorBasisPoints, postEntry.PostHash)); err != nil {

			return errors.Wrapf(err, "DbDeletePostEntryMappingsWithTxn: Deleting "+
				"creatorBps mapping for post hash %v: %v", postHash, err)
		}
		if err := DBDeleteWithTxn(txn, snap, _dbKeyForStakeMultipleBpsPostHash(
			postEntry.StakeMultipleBasisPoints, postEntry.PostHash)); err != nil {

			return errors.Wrapf(err, "DbDeletePostEntryMappingsWithTxn: Deleting "+
				"stakeMultiple mapping for post hash %v: %v", postHash, err)
		}
	}

	// Delete the repost entries for the post.
	if IsVanillaRepost(postEntry) {
		if err := DBDeleteWithTxn(txn, snap,
			_dbKeyForReposterPubKeyRepostedPostHashToRepostPostHash(postEntry.PosterPublicKey, *postEntry.RepostedPostHash)); err != nil {
			return errors.Wrapf(err, "DbDeletePostEntryMappingsWithTxn: Error problem deleting mapping for repostPostHash to ReposterPubKey: %v", err)
		}
		if err := DBDeleteWithTxn(txn, snap,
			_dbKeyForRepostedPostHashReposterPubKey(postEntry.RepostedPostHash, postEntry.PosterPublicKey)); err != nil {
			return errors.Wrapf(err, "DbDeletePostEntryMappingsWithTxn: Error problem adding "+
				"mapping for _dbKeyForRepostedPostHashReposterPubKey: %v", err)
		}
	} else if IsQuotedRepost(postEntry) {
		// Put quoted repost stuff.
		if err := DBDeleteWithTxn(txn, snap,
			_dbKeyForRepostedPostHashReposterPubKeyRepostPostHash(
				postEntry.RepostedPostHash, postEntry.PosterPublicKey, postEntry.PostHash)); err != nil {
			return errors.Wrapf(err, "DbDeletePostEntryMappingsWithTxn: Error problem adding "+
				"mapping for _dbKeyForRepostedPostHashReposterPubKeyRepostPostHash: %v", err)

		}
	}

	return nil
}

func DBDeletePostEntryMappings(handle *badger.DB, snap *Snapshot,
	postHash *BlockHash, params *DeSoParams) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBDeletePostEntryMappingsWithTxn(txn, snap, postHash, params)
	})
}

func DBPutPostEntryMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	postEntry *PostEntry, params *DeSoParams) error {

	if err := DBSetWithTxn(txn, snap, _dbKeyForPostEntryHash(
		postEntry.PostHash), postEntry.Encode()); err != nil {

		return errors.Wrapf(err, "DbPutPostEntryMappingsWithTxn: Problem "+
			"adding mapping for post: %v", postEntry.PostHash)
	}

	// If the post is a comment we store it in a separate index. Comments are
	// technically posts but they really should be treated as their own entity.
	// The only reason they're not actually implemented that way is so that we
	// get code re-use.
	isComment := len(postEntry.ParentStakeID) != 0
	if isComment {
		// Extend the parent stake ID, which is a block hash, to 33 bytes, which
		// is the length of a public key and the standard length we use for this
		// key.
		extendedStakeID := append([]byte{}, postEntry.ParentStakeID...)
		if len(extendedStakeID) == HashSizeBytes {
			extendedStakeID = append(extendedStakeID, 0x00)
		}
		if len(extendedStakeID) != btcec.PubKeyBytesLenCompressed {
			return fmt.Errorf("DbPutPostEntryMappingsWithTxn: extended "+
				"ParentStakeID %#v must have length %v",
				extendedStakeID, btcec.PubKeyBytesLenCompressed)
		}
		parentStakeIDKey := _dbKeyForCommentParentStakeIDToPostHash(
			extendedStakeID, postEntry.TimestampNanos, postEntry.PostHash)
		if err := DBSetWithTxn(txn, snap, parentStakeIDKey, []byte{}); err != nil {

			return errors.Wrapf(err, "DbPutPostEntryMappingsWithTxn: Problem "+
				"adding mapping for comment: %v: %v", postEntry, err)
		}

	} else {
		if err := DBSetWithTxn(txn, snap, _dbKeyForPosterPublicKeyTimestampPostHash(
			postEntry.PosterPublicKey, postEntry.TimestampNanos, postEntry.PostHash), []byte{}); err != nil {

			return errors.Wrapf(err, "DbPutPostEntryMappingsWithTxn: Problem "+
				"adding mapping for public key: %v: %v", postEntry, err)
		}
		if err := DBSetWithTxn(txn, snap, _dbKeyForTstampPostHash(
			postEntry.TimestampNanos, postEntry.PostHash), []byte{}); err != nil {

			return errors.Wrapf(err, "DbPutPostEntryMappingsWithTxn: Problem "+
				"adding mapping for tstamp: %v", postEntry)
		}
		if err := DBSetWithTxn(txn, snap, _dbKeyForCreatorBpsPostHash(
			postEntry.CreatorBasisPoints, postEntry.PostHash), []byte{}); err != nil {

			return errors.Wrapf(err, "DbPutPostEntryMappingsWithTxn: Problem "+
				"adding mapping for creatorBps: %v", postEntry)
		}
		if err := DBSetWithTxn(txn, snap, _dbKeyForStakeMultipleBpsPostHash(
			postEntry.StakeMultipleBasisPoints, postEntry.PostHash), []byte{}); err != nil {

			return errors.Wrapf(err, "DbPutPostEntryMappingsWithTxn: Problem "+
				"adding mapping for stakeMultipleBps: %v", postEntry)
		}
	}
	// We treat reposting the same for both comments and posts.
	// We only store repost entry mappings for vanilla reposts
	if IsVanillaRepost(postEntry) {
		repostEntry := RepostEntry{
			RepostPostHash:   postEntry.PostHash,
			RepostedPostHash: postEntry.RepostedPostHash,
			ReposterPubKey:   postEntry.PosterPublicKey,
		}
		if err := DBSetWithTxn(txn, snap,
			_dbKeyForReposterPubKeyRepostedPostHashToRepostPostHash(postEntry.PosterPublicKey, *postEntry.RepostedPostHash),
			repostEntry.Encode()); err != nil {
			return errors.Wrapf(err, "DbPutPostEntryMappingsWithTxn: Error problem adding mapping for repostPostHash to ReposterPubKey: %v", err)
		}
		if err := DBSetWithTxn(txn, snap,
			_dbKeyForRepostedPostHashReposterPubKey(postEntry.RepostedPostHash, postEntry.PosterPublicKey),
			[]byte{}); err != nil {
			return errors.Wrapf(err, "DbPutPostEntryMappingsWithTxn: Error problem adding "+
				"mapping for _dbKeyForRepostedPostHashReposterPubKey: %v", err)
		}
	} else if IsQuotedRepost(postEntry) {
		// Put quoted repost stuff.
		if err := DBSetWithTxn(txn, snap,
			_dbKeyForRepostedPostHashReposterPubKeyRepostPostHash(
				postEntry.RepostedPostHash, postEntry.PosterPublicKey, postEntry.PostHash),
			[]byte{}); err != nil {
			return errors.Wrapf(err, "DbPutPostEntryMappingsWithTxn: Error problem adding "+
				"mapping for _dbKeyForRepostedPostHashReposterPubKeyRepostPostHash: %v", err)
		}
	}
	return nil
}

func DBPutPostEntryMappings(handle *badger.DB, snap *Snapshot,
	postEntry *PostEntry, params *DeSoParams) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBPutPostEntryMappingsWithTxn(txn, snap, postEntry, params)
	})
}

// Specifying minTimestampNanos gives you all posts after minTimestampNanos
// Pass minTimestampNanos = 0 && maxTimestampNanos = 0 if you want all posts
// Setting maxTimestampNanos = 0, will default maxTimestampNanos to the current time.
func DBGetAllPostsAndCommentsForPublicKeyOrderedByTimestamp(handle *badger.DB,
	snap *Snapshot, publicKey []byte, fetchEntries bool, minTimestampNanos uint64, maxTimestampNanos uint64) (
	_tstamps []uint64, _postAndCommentHashes []*BlockHash, _postAndCommentEntries []*PostEntry, _err error) {

	tstampsFetched := []uint64{}
	postAndCommentHashesFetched := []*BlockHash{}
	postAndCommentEntriesFetched := []*PostEntry{}
	dbPrefixx := append([]byte{}, _PrefixPosterPublicKeyTimestampPostHash...)
	dbPrefixx = append(dbPrefixx, publicKey...)

	err := handle.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions

		opts.PrefetchValues = false

		// Go in reverse order since a larger count is better.
		opts.Reverse = true

		it := txn.NewIterator(opts)
		defer it.Close()
		// Since we iterate backwards, the prefix must be bigger than all possible
		// timestamps that could actually exist. We use eight bytes since the timestamp is
		// encoded as a 64-bit big-endian byte slice, which will be eight bytes long.
		maxBigEndianUint64Bytes := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
		prefix := append(dbPrefixx, maxBigEndianUint64Bytes...)

		// If we have a maxTimeStamp, we use that instead of the maxBigEndianUint64.
		if maxTimestampNanos != 0 {
			prefix = append(dbPrefixx, EncodeUint64(maxTimestampNanos)...)
		}

		for it.Seek(prefix); it.ValidForPrefix(dbPrefixx); it.Next() {
			rawKey := it.Item().Key()

			// Key should be
			// [prefix][posterPublicKey][Timestamp][PostHash]

			// Pull out the relevant fields
			timestampSizeBytes := 8
			keyWithoutPrefix := rawKey[1:]
			//posterPublicKey := keyWithoutPrefix[:HashSizeBytes]
			publicKeySizeBytes := HashSizeBytes + 1
			tstampNanos := DecodeUint64(keyWithoutPrefix[publicKeySizeBytes:(publicKeySizeBytes + timestampSizeBytes)])

			postHash := &BlockHash{}
			copy(postHash[:], keyWithoutPrefix[(publicKeySizeBytes+timestampSizeBytes):])

			if tstampNanos < minTimestampNanos {
				break
			}

			tstampsFetched = append(tstampsFetched, tstampNanos)
			postAndCommentHashesFetched = append(postAndCommentHashesFetched, postHash)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if !fetchEntries {
		return tstampsFetched, postAndCommentHashesFetched, nil, nil
	}

	for _, postHash := range postAndCommentHashesFetched {
		postEntry := DBGetPostEntryByPostHash(handle, snap, postHash)
		if postEntry == nil {
			return nil, nil, nil, fmt.Errorf("DBGetPostEntryByPostHash: "+
				"PostHash %v does not have corresponding entry", postHash)
		}
		postAndCommentEntriesFetched = append(postAndCommentEntriesFetched, postEntry)
	}

	return tstampsFetched, postAndCommentHashesFetched, postAndCommentEntriesFetched, nil
}

// DBGetAllPostsByTstamp returns all the posts in the db with the newest
// posts first.
//
// TODO(performance): This currently fetches all posts. We should implement
// some kind of pagination instead though.
func DBGetAllPostsByTstamp(handle *badger.DB, snap *Snapshot, fetchEntries bool) (
	_tstamps []uint64, _postHashes []*BlockHash, _postEntries []*PostEntry, _err error) {

	tstampsFetched := []uint64{}
	postHashesFetched := []*BlockHash{}
	postEntriesFetched := []*PostEntry{}
	dbPrefixx := append([]byte{}, _PrefixTstampNanosPostHash...)

	err := handle.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions

		opts.PrefetchValues = false

		// Go in reverse order since a larger count is better.
		opts.Reverse = true

		it := txn.NewIterator(opts)
		defer it.Close()
		// Since we iterate backwards, the prefix must be bigger than all possible
		// timestamps that could actually exist. We use eight bytes since the timestamp is
		// encoded as a 64-bit big-endian byte slice, which will be eight bytes long.
		maxBigEndianUint64Bytes := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
		prefix := append(dbPrefixx, maxBigEndianUint64Bytes...)
		for it.Seek(prefix); it.ValidForPrefix(dbPrefixx); it.Next() {
			rawKey := it.Item().Key()

			// Strip the prefix off the key and check its length. If it contains
			// a big-endian uint64 then it should be at least eight bytes.
			tstampPostHashKey := rawKey[1:]
			uint64BytesLen := len(maxBigEndianUint64Bytes)
			if len(tstampPostHashKey) != uint64BytesLen+HashSizeBytes {
				return fmt.Errorf("DBGetAllPostsByTstamp: Invalid key "+
					"length %d should be at least %d", len(tstampPostHashKey),
					uint64BytesLen+HashSizeBytes)
			}

			tstampNanos := DecodeUint64(tstampPostHashKey[:uint64BytesLen])

			// Appended to the tstamp should be the post hash so extract it here.
			postHash := &BlockHash{}
			copy(postHash[:], tstampPostHashKey[uint64BytesLen:])

			tstampsFetched = append(tstampsFetched, tstampNanos)
			postHashesFetched = append(postHashesFetched, postHash)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if !fetchEntries {
		return tstampsFetched, postHashesFetched, nil, nil
	}

	for _, postHash := range postHashesFetched {
		postEntry := DBGetPostEntryByPostHash(handle, snap, postHash)
		if postEntry == nil {
			return nil, nil, nil, fmt.Errorf("DBGetPostEntryByPostHash: "+
				"PostHash %v does not have corresponding entry", postHash)
		}
		postEntriesFetched = append(postEntriesFetched, postEntry)
	}

	return tstampsFetched, postHashesFetched, postEntriesFetched, nil
}

// DBGetCommentPostHashesForParentStakeID returns all the comments, which are indexed by their
// stake ID rather than by their timestamp.
//
// TODO(performance): This currently fetches all comments. We should implement
// something where we only get the comments for particular posts instead.
func DBGetCommentPostHashesForParentStakeID(
	handle *badger.DB, snap *Snapshot, stakeIDXXX []byte, fetchEntries bool) (
	_tstamps []uint64, _commentPostHashes []*BlockHash, _commentPostEntryes []*PostEntry, _err error) {

	tstampsFetched := []uint64{}
	commentPostHashes := []*BlockHash{}
	commentEntriesFetched := []*PostEntry{}
	dbPrefixx := append([]byte{}, _PrefixCommentParentStakeIDToPostHash...)
	dbPrefixx = append(dbPrefixx, stakeIDXXX...)

	err := handle.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions

		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()
		// Since we iterate backwards, the prefix must be bigger than all possible
		// counts that could actually exist. We use eight bytes since the count is
		// encoded as a 64-bit big-endian byte slice, which will be eight bytes long.
		maxBigEndianUint64Bytes := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
		//prefix := append(dbPrefixx, maxBigEndianUint64Bytes...)
		prefix := dbPrefixx
		for it.Seek(prefix); it.ValidForPrefix(dbPrefixx); it.Next() {
			rawKey := it.Item().Key()

			// Strip the prefix off the key and check its length. It should contain
			// a 33-byte stake id, an 8 byte tstamp, and a 32 byte comment hash.
			stakeIDTstampPostHashKey := rawKey[1:]
			uint64BytesLen := len(maxBigEndianUint64Bytes)
			if len(stakeIDTstampPostHashKey) != btcec.PubKeyBytesLenCompressed+uint64BytesLen+HashSizeBytes {
				return fmt.Errorf("DBGetCommentPostHashesForParentStakeID: Invalid key "+
					"length %d should be at least %d", len(stakeIDTstampPostHashKey),
					btcec.PubKeyBytesLenCompressed+uint64BytesLen+HashSizeBytes)
			}

			//stakeID := stakeIDTstampPostHashKey[:btcec.PubKeyBytesLenCompressed]
			tstampNanos := DecodeUint64(stakeIDTstampPostHashKey[btcec.PubKeyBytesLenCompressed : btcec.PubKeyBytesLenCompressed+uint64BytesLen])

			commentPostHashBytes := stakeIDTstampPostHashKey[btcec.PubKeyBytesLenCompressed+uint64BytesLen:]
			commentPostHash := &BlockHash{}
			copy(commentPostHash[:], commentPostHashBytes)

			//stakeIDsFetched = append(stakeIDsFetched, stakeID)
			tstampsFetched = append(tstampsFetched, tstampNanos)
			commentPostHashes = append(commentPostHashes, commentPostHash)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if !fetchEntries {
		return tstampsFetched, commentPostHashes, nil, nil
	}

	for _, postHash := range commentPostHashes {
		postEntry := DBGetPostEntryByPostHash(handle, snap, postHash)
		if postEntry == nil {
			return nil, nil, nil, fmt.Errorf("DBGetCommentPostHashesForParentStakeID: "+
				"PostHash %v does not have corresponding entry", postHash)
		}
		commentEntriesFetched = append(commentEntriesFetched, postEntry)
	}

	return tstampsFetched, commentPostHashes, commentEntriesFetched, nil
}

// =======================================================================================
// NFTEntry db functions
// =======================================================================================
func _dbKeyForNFTPostHashSerialNumber(nftPostHash *BlockHash, serialNumber uint64) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixPostHashSerialNumberToNFTEntry...)
	key := append(prefixCopy, nftPostHash[:]...)
	key = append(key, EncodeUint64(serialNumber)...)
	return key
}

func _dbKeyForPKIDIsForSaleBidAmountNanosNFTPostHashSerialNumber(pkid *PKID, isForSale bool, bidAmountNanos uint64, nftPostHash *BlockHash, serialNumber uint64) []byte {
	prefixCopy := append([]byte{}, _PrefixPKIDIsForSaleBidAmountNanosPostHashSerialNumberToNFTEntry...)
	key := append(prefixCopy, pkid[:]...)
	key = append(key, BoolToByte(isForSale))
	key = append(key, EncodeUint64(bidAmountNanos)...)
	key = append(key, nftPostHash[:]...)
	key = append(key, EncodeUint64(serialNumber)...)
	return key
}

func DBGetNFTEntryByPostHashSerialNumberWithTxn(txn *badger.Txn, snap *Snapshot,
	postHash *BlockHash, serialNumber uint64) *NFTEntry {

	key := _dbKeyForNFTPostHashSerialNumber(postHash, serialNumber)
	nftEntryBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	nftEntryObj := &NFTEntry{}
	nftEntryObj.Decode(nftEntryBytes)
	return nftEntryObj
}

func DBGetNFTEntryByPostHashSerialNumber(db *badger.DB, snap *Snapshot,
	postHash *BlockHash, serialNumber uint64) *NFTEntry {

	var ret *NFTEntry
	db.View(func(txn *badger.Txn) error {
		ret = DBGetNFTEntryByPostHashSerialNumberWithTxn(txn, snap, postHash, serialNumber)
		return nil
	})
	return ret
}

func DBDeleteNFTMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	nftPostHash *BlockHash, serialNumber uint64) error {

	// First pull up the mapping that exists for the post / serial # passed in.
	// If one doesn't exist then there's nothing to do.
	nftEntry := DBGetNFTEntryByPostHashSerialNumberWithTxn(txn, snap, nftPostHash, serialNumber)
	if nftEntry == nil {
		return nil
	}

	// When an nftEntry exists, delete the mapping.
	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForPKIDIsForSaleBidAmountNanosNFTPostHashSerialNumber(
			nftEntry.OwnerPKID, nftEntry.IsForSale, nftEntry.LastAcceptedBidAmountNanos, nftPostHash, serialNumber)); err != nil {
		return errors.Wrapf(err, "DbDeleteNFTMappingsWithTxn: Deleting "+
			"nft mapping for pkid %v post hash %v serial number %d", nftEntry.OwnerPKID, nftPostHash, serialNumber)
	}

	// When an nftEntry exists, delete the mapping.
	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForNFTPostHashSerialNumber(nftPostHash, serialNumber)); err != nil {
		return errors.Wrapf(err, "DbDeleteNFTMappingsWithTxn: Deleting "+
			"nft mapping for post hash %v serial number %d", nftPostHash, serialNumber)
	}

	return nil
}

func DBDeleteNFTMappings(
	handle *badger.DB, snap *Snapshot, postHash *BlockHash, serialNumber uint64) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBDeleteNFTMappingsWithTxn(txn, snap, postHash, serialNumber)
	})
}

func DBPutNFTEntryMappingsWithTxn(txn *badger.Txn, snap *Snapshot, nftEntry *NFTEntry) error {
	nftEntryBytes := nftEntry.Encode()

	if err := DBSetWithTxn(txn, snap, _dbKeyForNFTPostHashSerialNumber(
		nftEntry.NFTPostHash, nftEntry.SerialNumber), nftEntryBytes); err != nil {

		return errors.Wrapf(err, "DbPutNFTEntryMappingsWithTxn: Problem "+
			"adding mapping for post: %v, serial number: %d", nftEntry.NFTPostHash, nftEntry.SerialNumber)
	}

	if err := DBSetWithTxn(txn, snap, _dbKeyForPKIDIsForSaleBidAmountNanosNFTPostHashSerialNumber(
		nftEntry.OwnerPKID, nftEntry.IsForSale, nftEntry.LastAcceptedBidAmountNanos, nftEntry.NFTPostHash, nftEntry.SerialNumber), nftEntryBytes); err != nil {
		return errors.Wrapf(err, "DbPutNFTEntryMappingsWithTxn: Problem "+
			"adding mapping for pkid: %v, post: %v, serial number: %d", nftEntry.OwnerPKID, nftEntry.NFTPostHash, nftEntry.SerialNumber)
	}

	return nil
}

func DBPutNFTEntryMappings(handle *badger.DB, snap *Snapshot, nftEntry *NFTEntry) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBPutNFTEntryMappingsWithTxn(txn, snap, nftEntry)
	})
}

// DBGetNFTEntriesForPostHash gets NFT Entries *from the DB*. Does not include mempool txns.
func DBGetNFTEntriesForPostHash(handle *badger.DB, nftPostHash *BlockHash) (_nftEntries []*NFTEntry) {
	nftEntries := []*NFTEntry{}
	prefix := append([]byte{}, _PrefixPostHashSerialNumberToNFTEntry...)
	keyPrefix := append(prefix, nftPostHash[:]...)
	_, entryByteStringsFound := _enumerateKeysForPrefix(handle, keyPrefix)
	for _, byteString := range entryByteStringsFound {
		currentEntry := &NFTEntry{}
		currentEntry.Decode(byteString)
		nftEntries = append(nftEntries, currentEntry)
	}
	return nftEntries
}

// =======================================================================================
// NFTOwnership db functions
// NOTE: This index is not essential to running the protocol and should be computed
// outside of the protocol layer once update to the creation of TxIndex are complete.
// =======================================================================================

func DBGetNFTEntryByNFTOwnershipDetailsWithTxn(txn *badger.Txn, snap *Snapshot, ownerPKID *PKID,
	isForSale bool, bidAmountNanos uint64, postHash *BlockHash, serialNumber uint64) *NFTEntry {

	key := _dbKeyForPKIDIsForSaleBidAmountNanosNFTPostHashSerialNumber(ownerPKID, isForSale, bidAmountNanos, postHash, serialNumber)
	nftEntryBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	nftEntryObj := &NFTEntry{}
	nftEntryObj.Decode(nftEntryBytes)
	return nftEntryObj
}

func DBGetNFTEntryByNFTOwnershipDetails(db *badger.DB, snap *Snapshot, ownerPKID *PKID,
	isForSale bool, bidAmountNanos uint64, postHash *BlockHash, serialNumber uint64) *NFTEntry {

	var ret *NFTEntry
	db.View(func(txn *badger.Txn) error {
		ret = DBGetNFTEntryByNFTOwnershipDetailsWithTxn(txn, snap, ownerPKID, isForSale, bidAmountNanos, postHash, serialNumber)
		return nil
	})
	return ret
}

// DBGetNFTEntriesForPKID gets NFT Entries *from the DB*. Does not include mempool txns.
func DBGetNFTEntriesForPKID(handle *badger.DB, ownerPKID *PKID) (_nftEntries []*NFTEntry) {
	var nftEntries []*NFTEntry
	prefix := append([]byte{}, _PrefixPKIDIsForSaleBidAmountNanosPostHashSerialNumberToNFTEntry...)
	keyPrefix := append(prefix, ownerPKID[:]...)
	_, entryByteStringsFound := _enumerateKeysForPrefix(handle, keyPrefix)
	for _, byteString := range entryByteStringsFound {
		currentEntry := &NFTEntry{}
		currentEntry.Decode(byteString)
		nftEntries = append(nftEntries, currentEntry)
	}
	return nftEntries
}

// =======================================================================================
// AcceptedNFTBidEntries db functions
// NOTE: This index is not essential to running the protocol and should be computed
// outside of the protocol layer once update to the creation of TxIndex are complete.
// =======================================================================================

func EncodeAcceptedNFTBidEntries(nftBidEntries *[]*NFTBidEntry) []byte {
	var data []byte

	if nftBidEntries != nil {
		numEntries := uint64(len(*nftBidEntries))
		data = append(data, UintToBuf(numEntries)...)

		for _, entry := range *nftBidEntries {
			data = append(data, entry.Encode()...)
		}
	} else {
		data = append(data, UintToBuf(0)...)
	}

	return data
}

func DecodeAcceptedNFTBidEntries(data []byte) *[]*NFTBidEntry {
	var bidEntries []*NFTBidEntry
	rr := bytes.NewReader(data)

	numEntries, _ := ReadUvarint(rr)
	for ii := uint64(0); ii < numEntries; ii++ {
		bidEntry := &NFTBidEntry{}
		bidEntry.DecodeWithReader(rr)
		bidEntries = append(bidEntries, bidEntry)
	}

	return &bidEntries
}

func _dbKeyForPostHashSerialNumberToAcceptedBidEntries(nftPostHash *BlockHash, serialNumber uint64) []byte {
	prefixCopy := append([]byte{}, _PrefixPostHashSerialNumberToAcceptedBidEntries...)
	key := append(prefixCopy, nftPostHash[:]...)
	key = append(key, EncodeUint64(serialNumber)...)
	return key
}

func DBPutAcceptedNFTBidEntriesMappingWithTxn(txn *badger.Txn, snap *Snapshot,
	nftKey NFTKey, nftBidEntries *[]*NFTBidEntry) error {

	if err := DBSetWithTxn(txn, snap, _dbKeyForPostHashSerialNumberToAcceptedBidEntries(
		&nftKey.NFTPostHash, nftKey.SerialNumber), EncodeAcceptedNFTBidEntries(nftBidEntries)); err != nil {

		return errors.Wrapf(err, "DBPutAcceptedNFTBidEntriesMappingWithTxn: Problem "+
			"adding accepted bid mapping for post: %v, serial number: %d", nftKey.NFTPostHash, nftKey.SerialNumber)
	}
	return nil
}

func DBPutAcceptedNFTBidEntriesMapping(handle *badger.DB, snap *Snapshot,
	nftKey NFTKey, nftBidEntries *[]*NFTBidEntry) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBPutAcceptedNFTBidEntriesMappingWithTxn(txn, snap, nftKey, nftBidEntries)
	})
}

func DBGetAcceptedNFTBidEntriesByPostHashSerialNumberWithTxn(txn *badger.Txn, snap *Snapshot,
	postHash *BlockHash, serialNumber uint64) *[]*NFTBidEntry {

	key := _dbKeyForPostHashSerialNumberToAcceptedBidEntries(postHash, serialNumber)
	nftBidEntriesBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	var nftBidEntriesObj *[]*NFTBidEntry
	nftBidEntriesObj = DecodeAcceptedNFTBidEntries(nftBidEntriesBytes)
	return nftBidEntriesObj
}

func DBGetAcceptedNFTBidEntriesByPostHashSerialNumber(db *badger.DB, snap *Snapshot,
	postHash *BlockHash, serialNumber uint64) *[]*NFTBidEntry {

	var ret *[]*NFTBidEntry
	db.View(func(txn *badger.Txn) error {
		ret = DBGetAcceptedNFTBidEntriesByPostHashSerialNumberWithTxn(txn, snap, postHash, serialNumber)
		return nil
	})
	return ret
}

func DBDeleteAcceptedNFTBidEntriesMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	nftPostHash *BlockHash, serialNumber uint64) error {

	// First check to see if there is an existing mapping. If one doesn't exist, there's nothing to do.
	nftBidEntries := DBGetAcceptedNFTBidEntriesByPostHashSerialNumberWithTxn(txn, snap, nftPostHash, serialNumber)
	if nftBidEntries == nil {
		return nil
	}

	// When an nftEntry exists, delete both mapping.
	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForPostHashSerialNumberToAcceptedBidEntries(nftPostHash, serialNumber)); err != nil {
		return errors.Wrapf(err, "DBDeleteAcceptedNFTBidEntriesMappingsWithTxn: Deleting "+
			"accepted nft bid mapping for post hash %v serial number %d", nftPostHash, serialNumber)
	}

	return nil
}

func DBDeleteAcceptedNFTBidMappings(handle *badger.DB, snap *Snapshot,
	postHash *BlockHash, serialNumber uint64) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBDeleteAcceptedNFTBidEntriesMappingsWithTxn(txn, snap, postHash, serialNumber)
	})
}

// =======================================================================================
// NFTBidEntry db functions
// =======================================================================================

func _dbKeyForNFTPostHashSerialNumberBidNanosBidderPKID(bidEntry *NFTBidEntry) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixPostHashSerialNumberBidNanosBidderPKID...)
	key := append(prefixCopy, bidEntry.NFTPostHash[:]...)
	key = append(key, EncodeUint64(bidEntry.SerialNumber)...)
	key = append(key, EncodeUint64(bidEntry.BidAmountNanos)...)
	key = append(key, bidEntry.BidderPKID[:]...)
	return key
}

func _dbKeyForNFTBidderPKIDPostHashSerialNumber(
	bidderPKID *PKID, nftPostHash *BlockHash, serialNumber uint64) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixBidderPKIDPostHashSerialNumberToBidNanos...)
	key := append(prefixCopy, bidderPKID[:]...)
	key = append(key, nftPostHash[:]...)
	key = append(key, EncodeUint64(serialNumber)...)
	return key
}

func _dbSeekKeyForNFTBids(nftHash *BlockHash, serialNumber uint64) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixPostHashSerialNumberBidNanosBidderPKID...)
	key := append(prefixCopy, nftHash[:]...)
	key = append(key, EncodeUint64(serialNumber)...)
	return key
}

func DBGetNFTBidEntryForNFTBidKeyWithTxn(txn *badger.Txn, snap *Snapshot,
	nftBidKey *NFTBidKey) *NFTBidEntry {

	key := _dbKeyForNFTBidderPKIDPostHashSerialNumber(
		&nftBidKey.BidderPKID, &nftBidKey.NFTPostHash, nftBidKey.SerialNumber)

	nftBidBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	// If we get here then it means we actually had a bid amount for this key in the DB.
	nftBidAmountNanos := DecodeUint64(nftBidBytes)

	nftBidEntry := &NFTBidEntry{
		BidderPKID:     &nftBidKey.BidderPKID,
		NFTPostHash:    &nftBidKey.NFTPostHash,
		SerialNumber:   nftBidKey.SerialNumber,
		BidAmountNanos: nftBidAmountNanos,
	}

	return nftBidEntry
}

func DBGetNFTBidEntryForNFTBidKey(db *badger.DB, snap *Snapshot, nftBidKey *NFTBidKey) *NFTBidEntry {
	var ret *NFTBidEntry
	db.View(func(txn *badger.Txn) error {
		ret = DBGetNFTBidEntryForNFTBidKeyWithTxn(txn, snap, nftBidKey)
		return nil
	})
	return ret
}

func DBDeleteNFTBidMappingsWithTxn(txn *badger.Txn, snap *Snapshot, nftBidKey *NFTBidKey) error {

	// First check to see if there is an existing mapping. If one doesn't exist, there's nothing to do.
	nftBidEntry := DBGetNFTBidEntryForNFTBidKeyWithTxn(txn, snap, nftBidKey)
	if nftBidEntry == nil {
		return nil
	}

	// When an nftEntry exists, delete both mapping.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForNFTPostHashSerialNumberBidNanosBidderPKID(nftBidEntry)); err != nil {
		return errors.Wrapf(err, "DbDeleteNFTBidMappingsWithTxn: Deleting "+
			"nft bid mapping for nftBidKey %v", nftBidKey)
	}

	// When an nftEntry exists, delete both mapping.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForNFTBidderPKIDPostHashSerialNumber(
		nftBidEntry.BidderPKID, nftBidEntry.NFTPostHash, nftBidEntry.SerialNumber)); err != nil {
		return errors.Wrapf(err, "DbDeleteNFTBidMappingsWithTxn: Deleting "+
			"nft bid mapping for nftBidKey %v", nftBidKey)
	}

	return nil
}

func DBDeleteNFTBidMappings(handle *badger.DB, snap *Snapshot, nftBidKey *NFTBidKey) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBDeleteNFTBidMappingsWithTxn(txn, snap, nftBidKey)
	})
}

func DBPutNFTBidEntryMappingsWithTxn(txn *badger.Txn, snap *Snapshot, nftBidEntry *NFTBidEntry) error {
	// We store two indexes for NFT bids. (1) sorted by bid amount nanos in the key and
	// (2) sorted by the bidder PKID. Both come in handy.

	// Put the first index --> []byte{} (no data needs to be stored since it all info is in the key)
	if err := DBSetWithTxn(txn, snap,
		_dbKeyForNFTPostHashSerialNumberBidNanosBidderPKID(nftBidEntry), []byte{}); err != nil {

		return errors.Wrapf(err, "DbPutNFTBidEntryMappingsWithTxn: Problem "+
			"adding mapping to BidderPKID for bid entry: %v", nftBidEntry)
	}

	// Put the second index --> BidAmountNanos
	if err := DBSetWithTxn(txn, snap, _dbKeyForNFTBidderPKIDPostHashSerialNumber(
		nftBidEntry.BidderPKID, nftBidEntry.NFTPostHash, nftBidEntry.SerialNumber,
	), EncodeUint64(nftBidEntry.BidAmountNanos)); err != nil {

		return errors.Wrapf(err, "DbPutNFTBidEntryMappingsWithTxn: Problem "+
			"adding mapping to BidAmountNanos for bid entry: %v", nftBidEntry)
	}

	return nil
}

func DBPutNFTBidEntryMappings(handle *badger.DB, snap *Snapshot, nftEntry *NFTBidEntry) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBPutNFTBidEntryMappingsWithTxn(txn, snap, nftEntry)
	})
}

func DBGetNFTBidEntriesForPKID(handle *badger.DB, bidderPKID *PKID) (_nftBidEntries []*NFTBidEntry) {
	nftBidEntries := []*NFTBidEntry{}
	{
		prefix := append([]byte{}, _PrefixBidderPKIDPostHashSerialNumberToBidNanos...)
		keyPrefix := append(prefix, bidderPKID[:]...)
		keysFound, valuesFound := _enumerateKeysForPrefix(handle, keyPrefix)
		bidderPKIDLength := len(bidderPKID[:])
		for ii, keyFound := range keysFound {

			postHashStartIdx := 1 + bidderPKIDLength           // The length of prefix + length of PKID
			postHashEndIdx := postHashStartIdx + HashSizeBytes // Add the length of the bid amount (uint64).

			// Cut the bid amount out of the key and decode.
			postHashBytes := keyFound[postHashStartIdx:postHashEndIdx]

			nftHash := &BlockHash{}
			copy(nftHash[:], postHashBytes)

			serialNumber := DecodeUint64(keyFound[postHashEndIdx:])

			bidAmountNanos := DecodeUint64(valuesFound[ii])

			currentEntry := &NFTBidEntry{
				NFTPostHash:    nftHash,
				SerialNumber:   serialNumber,
				BidderPKID:     bidderPKID,
				BidAmountNanos: bidAmountNanos,
			}
			nftBidEntries = append(nftBidEntries, currentEntry)
		}
	}
	return nftBidEntries
}

// Get NFT bid Entries *from the DB*. Does not include mempool txns.
func DBGetNFTBidEntries(handle *badger.DB, nftPostHash *BlockHash, serialNumber uint64,
) (_nftBidEntries []*NFTBidEntry) {
	nftBidEntries := []*NFTBidEntry{}
	{
		prefix := append([]byte{}, _PrefixPostHashSerialNumberBidNanosBidderPKID...)
		keyPrefix := append(prefix, nftPostHash[:]...)
		keyPrefix = append(keyPrefix, EncodeUint64(serialNumber)...)
		keysFound, _ := _enumerateKeysForPrefix(handle, keyPrefix)
		for _, keyFound := range keysFound {
			bidAmountStartIdx := 1 + HashSizeBytes + 8 // The length of prefix + the post hash + the serial #.
			bidAmountEndIdx := bidAmountStartIdx + 8   // Add the length of the bid amount (uint64).

			// Cut the bid amount out of the key and decode.
			bidAmountBytes := keyFound[bidAmountStartIdx:bidAmountEndIdx]
			bidAmountNanos := DecodeUint64(bidAmountBytes)

			// Cut the pkid bytes out of the keys
			bidderPKIDBytes := keyFound[bidAmountEndIdx:]

			// Construct the bidder PKID.
			bidderPKID := PublicKeyToPKID(bidderPKIDBytes)

			currentEntry := &NFTBidEntry{
				NFTPostHash:    nftPostHash,
				SerialNumber:   serialNumber,
				BidderPKID:     bidderPKID,
				BidAmountNanos: bidAmountNanos,
			}
			nftBidEntries = append(nftBidEntries, currentEntry)
		}
	}
	return nftBidEntries
}

func DBGetNFTBidEntriesPaginated(
	handle *badger.DB,
	nftHash *BlockHash,
	serialNumber uint64,
	startEntry *NFTBidEntry,
	limit int,
	reverse bool,
) (_bidEntries []*NFTBidEntry) {
	seekKey := _dbSeekKeyForNFTBids(nftHash, serialNumber)
	startKey := seekKey
	if startEntry != nil {
		startKey = _dbKeyForNFTPostHashSerialNumberBidNanosBidderPKID(startEntry)
	}
	// The key length consists of: (1 prefix byte) + (BlockHash) + (2 x uint64) + (PKID)
	maxKeyLen := 1 + HashSizeBytes + 16 + btcec.PubKeyBytesLenCompressed
	keysBytes, _, _ := DBGetPaginatedKeysAndValuesForPrefix(
		handle,
		startKey,
		seekKey,
		maxKeyLen,
		limit,
		reverse,
		false)
	// TODO: We should probably handle the err case for this function.

	// Chop up the keyBytes into bid entries.
	var bidEntries []*NFTBidEntry
	for _, keyBytes := range keysBytes {
		serialNumStartIdx := 1 + HashSizeBytes
		bidAmountStartIdx := serialNumStartIdx + 8
		bidderPKIDStartIdx := bidAmountStartIdx + 8

		nftHashBytes := keyBytes[1:serialNumStartIdx]
		serialNumberBytes := keyBytes[serialNumStartIdx:bidAmountStartIdx]
		bidAmountBytes := keyBytes[bidAmountStartIdx:bidderPKIDStartIdx]
		bidderPKIDBytes := keyBytes[bidderPKIDStartIdx:]

		nftHash := &BlockHash{}
		copy(nftHash[:], nftHashBytes)
		serialNumber := DecodeUint64(serialNumberBytes)
		bidAmount := DecodeUint64(bidAmountBytes)
		bidderPKID := &PKID{}
		copy(bidderPKID[:], bidderPKIDBytes)

		bidEntry := &NFTBidEntry{
			NFTPostHash:    nftHash,
			SerialNumber:   serialNumber,
			BidAmountNanos: bidAmount,
			BidderPKID:     bidderPKID,
		}

		bidEntries = append(bidEntries, bidEntry)
	}

	return bidEntries
}

// ======================================================================================
// Authorize derived key functions
//  	<prefix, owner pub key [33]byte, derived pub key [33]byte> -> <DerivedKeyEntry>
// ======================================================================================

func _dbKeyForOwnerToDerivedKeyMapping(
	ownerPublicKey PublicKey, derivedPublicKey PublicKey) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixAuthorizeDerivedKey...)
	key := append(prefixCopy, ownerPublicKey[:]...)
	key = append(key, derivedPublicKey[:]...)
	return key
}

func _dbSeekPrefixForDerivedKeyMappings(
	ownerPublicKey PublicKey) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixAuthorizeDerivedKey...)
	key := append(prefixCopy, ownerPublicKey[:]...)
	return key
}

func DBPutDerivedKeyMappingWithTxn(txn *badger.Txn, snap *Snapshot,
	ownerPublicKey PublicKey, derivedPublicKey PublicKey, derivedKeyEntry *DerivedKeyEntry) error {

	if len(ownerPublicKey.ToBytes()) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DBPutDerivedKeyMappingsWithTxn: Owner Public Key "+
			"length %d != %d", len(ownerPublicKey), btcec.PubKeyBytesLenCompressed)
	}
	if len(derivedPublicKey.ToBytes()) != btcec.PubKeyBytesLenCompressed {
		return fmt.Errorf("DBPutDerivedKeyMappingsWithTxn: Derived Public Key "+
			"length %d != %d", len(derivedPublicKey), btcec.PubKeyBytesLenCompressed)
	}

	key := _dbKeyForOwnerToDerivedKeyMapping(ownerPublicKey, derivedPublicKey)

	return DBSetWithTxn(txn, snap, key, derivedKeyEntry.Encode())
}

func DBPutDerivedKeyMapping(handle *badger.DB, snap *Snapshot,
	ownerPublicKey PublicKey, derivedPublicKey PublicKey, derivedKeyEntry *DerivedKeyEntry) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBPutDerivedKeyMappingWithTxn(txn, snap, ownerPublicKey, derivedPublicKey, derivedKeyEntry)
	})
}

func DBGetOwnerToDerivedKeyMappingWithTxn(txn *badger.Txn, snap *Snapshot,
	ownerPublicKey PublicKey, derivedPublicKey PublicKey) *DerivedKeyEntry {

	key := _dbKeyForOwnerToDerivedKeyMapping(ownerPublicKey, derivedPublicKey)
	derivedKeyBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	derivedKeyEntry := &DerivedKeyEntry{}
	derivedKeyEntry.Decode(derivedKeyBytes)
	return derivedKeyEntry
}

func DBGetOwnerToDerivedKeyMapping(db *badger.DB, snap *Snapshot,
	ownerPublicKey PublicKey, derivedPublicKey PublicKey) *DerivedKeyEntry {

	var derivedKeyEntry *DerivedKeyEntry
	db.View(func(txn *badger.Txn) error {
		derivedKeyEntry = DBGetOwnerToDerivedKeyMappingWithTxn(txn, snap, ownerPublicKey, derivedPublicKey)
		return nil
	})
	return derivedKeyEntry
}

func DBDeleteDerivedKeyMappingWithTxn(txn *badger.Txn, snap *Snapshot,
	ownerPublicKey PublicKey, derivedPublicKey PublicKey) error {

	// First check that a mapping exists for the passed in public keys.
	// If one doesn't exist then there's nothing to do.
	//derivedKeyEntry := DBGetOwnerToDerivedKeyMappingWithTxn(
	//	txn, ownerPublicKey, derivedPublicKey)
	//if derivedKeyEntry == nil {
	//	return nil
	//}

	// When a mapping exists, delete it.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForOwnerToDerivedKeyMapping(ownerPublicKey, derivedPublicKey)); err != nil {
		return errors.Wrapf(err, "DBDeleteDerivedKeyMappingWithTxn: Deleting "+
			"ownerPublicKey %s and derivedPublicKey %s failed",
			PkToStringMainnet(ownerPublicKey[:]), PkToStringMainnet(derivedPublicKey[:]))
	}

	return nil
}

func DBDeleteDerivedKeyMapping(handle *badger.DB, snap *Snapshot,
	ownerPublicKey PublicKey, derivedPublicKey PublicKey) error {
	return handle.Update(func(txn *badger.Txn) error {
		return DBDeleteDerivedKeyMappingWithTxn(txn, snap, ownerPublicKey, derivedPublicKey)
	})
}

func DBGetAllOwnerToDerivedKeyMappings(handle *badger.DB, ownerPublicKey PublicKey) (
	_entries []*DerivedKeyEntry, _err error) {

	prefix := _dbSeekPrefixForDerivedKeyMappings(ownerPublicKey)
	_, valsFound := _enumerateKeysForPrefix(handle, prefix)

	var derivedEntries []*DerivedKeyEntry
	for _, keyBytes := range valsFound {
		derivedKeyEntry := &DerivedKeyEntry{}
		err := gob.NewDecoder(bytes.NewReader(keyBytes)).Decode(derivedKeyEntry)
		if err != nil {
			return nil, err
		}
		derivedEntries = append(derivedEntries, derivedKeyEntry)
	}

	return derivedEntries, nil
}

// ======================================================================================
// Profile code
// ======================================================================================
func _dbKeyForPKIDToProfileEntry(pkid *PKID) []byte {
	prefixCopy := append([]byte{}, _PrefixPKIDToProfileEntry...)
	key := append(prefixCopy, pkid[:]...)
	return key
}
func _dbKeyForProfileUsernameToPKID(nonLowercaseUsername []byte) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	key := append([]byte{}, _PrefixProfileUsernameToPKID...)
	// Always lowercase the username when we use it as a key in our db. This allows
	// us to check uniqueness in a case-insensitive way.
	lowercaseUsername := []byte(strings.ToLower(string(nonLowercaseUsername)))
	key = append(key, lowercaseUsername...)
	return key
}

// This is the key we use to sort profiles by their amount of DeSo locked
func _dbKeyForCreatorDeSoLockedNanosCreatorPKID(desoLockedNanos uint64, pkid *PKID) []byte {
	key := append([]byte{}, _PrefixCreatorDeSoLockedNanosCreatorPKID...)
	key = append(key, EncodeUint64(desoLockedNanos)...)
	key = append(key, pkid[:]...)
	return key
}

func DbPrefixForCreatorDeSoLockedNanosCreatorPKID() []byte {
	return append([]byte{}, _PrefixCreatorDeSoLockedNanosCreatorPKID...)
}

func DBGetPKIDForUsernameWithTxn(txn *badger.Txn,
	snap *Snapshot, username []byte) *PKID {

	key := _dbKeyForProfileUsernameToPKID(username)
	profileBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	return PublicKeyToPKID(profileBytes)
}

func DBGetPKIDForUsername(db *badger.DB, snap *Snapshot, username []byte) *PKID {
	var ret *PKID
	db.View(func(txn *badger.Txn) error {
		ret = DBGetPKIDForUsernameWithTxn(txn, snap, username)
		return nil
	})
	return ret
}

func DBGetProfileEntryForUsernameWithTxn(txn *badger.Txn,
	snap *Snapshot, username []byte) *ProfileEntry {

	pkid := DBGetPKIDForUsernameWithTxn(txn, snap, username)
	if pkid == nil {
		return nil
	}

	return DBGetProfileEntryForPKIDWithTxn(txn, snap, pkid)
}

func DBGetProfileEntryForUsername(db *badger.DB, snap *Snapshot, username []byte) *ProfileEntry {
	var ret *ProfileEntry
	db.View(func(txn *badger.Txn) error {
		ret = DBGetProfileEntryForUsernameWithTxn(txn, snap, username)
		return nil
	})
	return ret
}

func DBGetProfileEntryForPKIDWithTxn(txn *badger.Txn, snap *Snapshot,
	pkid *PKID) *ProfileEntry {

	key := _dbKeyForPKIDToProfileEntry(pkid)
	profileEntryBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	profileEntryObj := &ProfileEntry{}
	profileEntryObj.Decode(profileEntryBytes)
	return profileEntryObj
}

func DBGetProfileEntryForPKID(db *badger.DB, snap *Snapshot, pkid *PKID) *ProfileEntry {
	var ret *ProfileEntry
	db.View(func(txn *badger.Txn) error {
		ret = DBGetProfileEntryForPKIDWithTxn(txn, snap, pkid)
		return nil
	})
	return ret
}

func DBDeleteProfileEntryMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	pkid *PKID, params *DeSoParams) error {

	// First pull up the mapping that exists for the profile pub key passed in.
	// If one doesn't exist then there's nothing to do.
	profileEntry := DBGetProfileEntryForPKIDWithTxn(txn, snap, pkid)
	if profileEntry == nil {
		return nil
	}

	// When a profile exists, delete the pkid mapping for the profile.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForPKIDToProfileEntry(pkid)); err != nil {
		return errors.Wrapf(err, "DbDeleteProfileEntryMappingsWithTxn: Deleting "+
			"profile mapping for profile PKID: %v",
			PkToString(pkid[:], params))
	}

	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForProfileUsernameToPKID(profileEntry.Username)); err != nil {

		return errors.Wrapf(err, "DbDeleteProfileEntryMappingsWithTxn: Deleting "+
			"username mapping for profile username %v", string(profileEntry.Username))
	}

	// The coin deso mapping
	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForCreatorDeSoLockedNanosCreatorPKID(
			profileEntry.DeSoLockedNanos, pkid)); err != nil {

		return errors.Wrapf(err, "DbDeleteProfileEntryMappingsWithTxn: Deleting "+
			"coin mapping for profile username %v", string(profileEntry.Username))
	}

	return nil
}

func DBPutProfileEntryMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	profileEntry *ProfileEntry, pkid *PKID, params *DeSoParams) error {

	// Set the main PKID -> profile entry mapping.
	if err := DBSetWithTxn(txn, snap, _dbKeyForPKIDToProfileEntry(pkid), profileEntry.Encode()); err != nil {

		return errors.Wrapf(err, "DbPutProfileEntryMappingsWithTxn: Problem "+
			"adding mapping for profile: %v", PkToString(pkid[:], params))
	}

	// Username
	if err := DBSetWithTxn(txn, snap,
		_dbKeyForProfileUsernameToPKID(profileEntry.Username),
		pkid[:]); err != nil {

		return errors.Wrapf(err, "DbPutProfileEntryMappingsWithTxn: Problem "+
			"adding mapping for profile with username: %v", string(profileEntry.Username))
	}

	// The coin deso mapping
	if err := DBSetWithTxn(txn, snap,
		_dbKeyForCreatorDeSoLockedNanosCreatorPKID(
			profileEntry.DeSoLockedNanos, pkid), []byte{}); err != nil {

		return errors.Wrapf(err, "DbPutProfileEntryMappingsWithTxn: Problem "+
			"adding mapping for profile coin: ")
	}

	return nil
}

func DBPutProfileEntryMappings(handle *badger.DB, snap *Snapshot,
	profileEntry *ProfileEntry, pkid *PKID, params *DeSoParams) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBPutProfileEntryMappingsWithTxn(txn, snap, profileEntry, pkid, params)
	})
}

// DBGetAllProfilesByCoinValue returns all the profiles in the db with the
// highest coin values first.
//
// TODO(performance): This currently fetches all profiles. We should implement
// some kind of pagination instead though.
func DBGetAllProfilesByCoinValue(handle *badger.DB, snap *Snapshot, fetchEntries bool) (
	_lockedDeSoNanos []uint64, _profilePublicKeys []*PKID,
	_profileEntries []*ProfileEntry, _err error) {

	lockedDeSoNanosFetched := []uint64{}
	profilePublicKeysFetched := []*PKID{}
	profileEntriesFetched := []*ProfileEntry{}
	dbPrefixx := append([]byte{}, _PrefixCreatorDeSoLockedNanosCreatorPKID...)

	err := handle.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions

		opts.PrefetchValues = false

		// Go in reverse order since a larger count is better.
		opts.Reverse = true

		it := txn.NewIterator(opts)
		defer it.Close()
		// Since we iterate backwards, the prefix must be bigger than all possible
		// counts that could actually exist. We use eight bytes since the count is
		// encoded as a 64-bit big-endian byte slice, which will be eight bytes long.
		maxBigEndianUint64Bytes := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
		prefix := append(dbPrefixx, maxBigEndianUint64Bytes...)
		for it.Seek(prefix); it.ValidForPrefix(dbPrefixx); it.Next() {
			rawKey := it.Item().Key()

			// Strip the prefix off the key and check its length. If it contains
			// a big-endian uint64 then it should be at least eight bytes.
			lockedDeSoPubKeyConcatKey := rawKey[1:]
			uint64BytesLen := len(maxBigEndianUint64Bytes)
			expectedLength := uint64BytesLen + btcec.PubKeyBytesLenCompressed
			if len(lockedDeSoPubKeyConcatKey) != expectedLength {
				return fmt.Errorf("DBGetAllProfilesByLockedDeSo: Invalid key "+
					"length %d should be at least %d", len(lockedDeSoPubKeyConcatKey),
					expectedLength)
			}

			lockedDeSoNanos := DecodeUint64(lockedDeSoPubKeyConcatKey[:uint64BytesLen])

			// Appended to the stake should be the profile pub key so extract it here.
			profilePKID := make([]byte, btcec.PubKeyBytesLenCompressed)
			copy(profilePKID[:], lockedDeSoPubKeyConcatKey[uint64BytesLen:])

			lockedDeSoNanosFetched = append(lockedDeSoNanosFetched, lockedDeSoNanos)
			profilePublicKeysFetched = append(profilePublicKeysFetched, PublicKeyToPKID(profilePKID))
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if !fetchEntries {
		return lockedDeSoNanosFetched, profilePublicKeysFetched, nil, nil
	}

	for _, profilePKID := range profilePublicKeysFetched {
		profileEntry := DBGetProfileEntryForPKID(handle, snap, profilePKID)
		if profileEntry == nil {
			return nil, nil, nil, fmt.Errorf("DBGetAllProfilesByLockedDeSo: "+
				"ProfilePubKey %v does not have corresponding entry",
				PkToStringBoth(profilePKID[:]))
		}
		profileEntriesFetched = append(profileEntriesFetched, profileEntry)
	}

	return lockedDeSoNanosFetched, profilePublicKeysFetched, profileEntriesFetched, nil
}

// =====================================================================================
// Creator coin balance entry code
// =====================================================================================
func _dbKeyForHODLerPKIDCreatorPKIDToBalanceEntry(hodlerPKID *PKID, creatorPKID *PKID) []byte {
	key := append([]byte{}, _PrefixHODLerPKIDCreatorPKIDToBalanceEntry...)
	key = append(key, hodlerPKID[:]...)
	key = append(key, creatorPKID[:]...)
	return key
}
func _dbKeyForCreatorPKIDHODLerPKIDToBalanceEntry(creatorPKID *PKID, hodlerPKID *PKID) []byte {
	key := append([]byte{}, _PrefixCreatorPKIDHODLerPKIDToBalanceEntry...)
	key = append(key, creatorPKID[:]...)
	key = append(key, hodlerPKID[:]...)
	return key
}

func DBGetCreatorCoinBalanceEntryForHODLerAndCreatorPKIDsWithTxn(
	txn *badger.Txn, snap *Snapshot, hodlerPKID *PKID, creatorPKID *PKID) *BalanceEntry {

	key := _dbKeyForHODLerPKIDCreatorPKIDToBalanceEntry(hodlerPKID, creatorPKID)
	balanceEntryBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	balanceEntryObj := &BalanceEntry{}
	balanceEntryObj.Decode(balanceEntryBytes)
	return balanceEntryObj
}

func DBGetCreatorCoinBalanceEntryForHODLerAndCreatorPKIDs(handle *badger.DB,
	snap *Snapshot, hodlerPKID *PKID, creatorPKID *PKID) *BalanceEntry {

	var ret *BalanceEntry
	handle.View(func(txn *badger.Txn) error {
		ret = DBGetCreatorCoinBalanceEntryForHODLerAndCreatorPKIDsWithTxn(
			txn, snap, hodlerPKID, creatorPKID)
		return nil
	})
	return ret
}

func DBDeleteCreatorCoinBalanceEntryMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	hodlerPKID *PKID, creatorPKID *PKID) error {

	// First pull up the mappings that exists for the keys passed in.
	// If one doesn't exist then there's nothing to do.
	//balanceEntry := DBGetCreatorCoinBalanceEntryForHODLerAndCreatorPKIDsWithTxn(
	//	txn, hodlerPKID, creatorPKID)
	//if balanceEntry == nil {
	//	return nil
	//}

	// When an entry exists, delete the mappings for it.
	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForHODLerPKIDCreatorPKIDToBalanceEntry(hodlerPKID, creatorPKID)); err != nil {
		return errors.Wrapf(err, "DbDeleteCreatorCoinBalanceEntryMappingsWithTxn: Deleting "+
			"mappings with keys: %v %v", PkToStringBoth(hodlerPKID[:]), PkToStringBoth(creatorPKID[:]))
	}
	if err := DBDeleteWithTxn(txn, snap,
		_dbKeyForCreatorPKIDHODLerPKIDToBalanceEntry(creatorPKID, hodlerPKID)); err != nil {
		return errors.Wrapf(err, "DbDeleteCreatorCoinBalanceEntryMappingsWithTxn: Deleting "+
			"mappings with keys: %v %v", PkToStringBoth(hodlerPKID[:]), PkToStringBoth(creatorPKID[:]))
	}

	// Note: We don't update the CreatorDeSoLockedNanosCreatorPubKeyIIndex
	// because we expect that the caller is keeping the individual holdings in
	// sync with the "total" coins stored in the profile.

	return nil
}

func DBDeleteCreatorCoinBalanceEntryMappings(handle *badger.DB, snap *Snapshot,
	hodlerPKID *PKID, creatorPKID *PKID) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBDeleteCreatorCoinBalanceEntryMappingsWithTxn(
			txn, snap, hodlerPKID, creatorPKID)
	})
}

func DBPutCreatorCoinBalanceEntryMappingsWithTxn(txn *badger.Txn, snap *Snapshot,
	balanceEntry *BalanceEntry) error {

	balanceEntryBytes := balanceEntry.Encode()
	// Set the forward direction for the HODLer
	if err := DBSetWithTxn(txn, snap, _dbKeyForHODLerPKIDCreatorPKIDToBalanceEntry(
		balanceEntry.HODLerPKID, balanceEntry.CreatorPKID),
		balanceEntryBytes); err != nil {

		return errors.Wrapf(err, "DbPutCreatorCoinBalanceEntryMappingsWithTxn: Problem "+
			"adding forward mappings for pub keys: %v %v",
			PkToStringBoth(balanceEntry.HODLerPKID[:]),
			PkToStringBoth(balanceEntry.CreatorPKID[:]))
	}

	// Set the reverse direction for the creator
	if err := DBSetWithTxn(txn, snap, _dbKeyForCreatorPKIDHODLerPKIDToBalanceEntry(
		balanceEntry.CreatorPKID, balanceEntry.HODLerPKID),
		balanceEntryBytes); err != nil {

		return errors.Wrapf(err, "DbPutCreatorCoinBalanceEntryMappingsWithTxn: Problem "+
			"adding reverse mappings for pub keys: %v %v",
			PkToStringBoth(balanceEntry.HODLerPKID[:]),
			PkToStringBoth(balanceEntry.CreatorPKID[:]))
	}

	return nil
}

func DBPutCreatorCoinBalanceEntryMappings(handle *badger.DB, snap *Snapshot,
	balanceEntry *BalanceEntry) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DBPutCreatorCoinBalanceEntryMappingsWithTxn(txn, snap, balanceEntry)
	})
}

// GetSingleBalanceEntryFromPublicKeys fetchs a single balance entry of a holder's creator coin.
// Returns nil if the balance entry never existed.
// TODO: This is suboptimal, shouldn't be passing UtxoView
func GetSingleBalanceEntryFromPublicKeys(holder []byte, creator []byte, utxoView *UtxoView) (*BalanceEntry, error) {
	holderPKIDEntry := utxoView.GetPKIDForPublicKey(holder)
	if holderPKIDEntry == nil || holderPKIDEntry.isDeleted {
		return nil, fmt.Errorf("DbGetSingleBalanceEntryFromPublicKeys: holderPKID was nil or deleted; this should never happen")
	}
	holderPKID := holderPKIDEntry.PKID
	creatorPKIDEntry := utxoView.GetPKIDForPublicKey(creator)
	if creatorPKIDEntry == nil || creatorPKIDEntry.isDeleted {
		return nil, fmt.Errorf("DbGetSingleBalanceEntryFromPublicKeys: creatorPKID was nil or deleted; this should never happen")
	}
	creatorPKID := creatorPKIDEntry.PKID

	// Check if there's a balance entry in the view
	balanceEntryMapKey := BalanceEntryMapKey{HODLerPKID: *holderPKID, CreatorPKID: *creatorPKID}
	balanceEntryFromView, _ := utxoView.HODLerPKIDCreatorPKIDToBalanceEntry[balanceEntryMapKey]
	if balanceEntryFromView != nil {
		return balanceEntryFromView, nil
	}

	// Check if there's a balance entry in the database
	balanceEntryFromDb := DbGetBalanceEntry(utxoView.Handle, utxoView.Snapshot, holderPKID, creatorPKID)
	return balanceEntryFromDb, nil
}

// DbGetBalanceEntry returns a balance entry from the database
func DbGetBalanceEntry(db *badger.DB, snap *Snapshot, holder *PKID, creator *PKID) *BalanceEntry {
	var ret *BalanceEntry
	db.View(func(txn *badger.Txn) error {
		ret = DbGetHolderPKIDCreatorPKIDToBalanceEntryWithTxn(txn, snap, holder, creator)
		return nil
	})
	return ret
}

func DbGetHolderPKIDCreatorPKIDToBalanceEntryWithTxn(txn *badger.Txn, snap *Snapshot,
	holder *PKID, creator *PKID) *BalanceEntry {

	key := _dbKeyForCreatorPKIDHODLerPKIDToBalanceEntry(creator, holder)
	balanceEntryBytes, err := DBGetWithTxn(txn, snap, key)
	if err != nil {
		return nil
	}

	balanceEntryObj := &BalanceEntry{}
	balanceEntryObj.Decode(balanceEntryBytes)
	return balanceEntryObj
}

// DbGetBalanceEntriesHodlingYou fetchs the BalanceEntries that the passed in pkid holds.
func DbGetBalanceEntriesYouHold(db *badger.DB, pkid *PKID, filterOutZeroBalances bool) ([]*BalanceEntry, error) {
	// Get the balance entries for the coins that *you hold*
	balanceEntriesYouHodl := []*BalanceEntry{}
	{
		prefix := append([]byte{}, _PrefixHODLerPKIDCreatorPKIDToBalanceEntry...)
		keyPrefix := append(prefix, pkid[:]...)
		_, entryByteStringsFound := _enumerateKeysForPrefix(db, keyPrefix)
		for _, byteString := range entryByteStringsFound {
			currentEntry := &BalanceEntry{}
			currentEntry.Decode(byteString)
			if filterOutZeroBalances && currentEntry.BalanceNanos == 0 {
				continue
			}
			balanceEntriesYouHodl = append(balanceEntriesYouHodl, currentEntry)
		}
	}

	return balanceEntriesYouHodl, nil
}

// DbGetBalanceEntriesHodlingYou fetches the BalanceEntries that hold the pkid passed in.
func DbGetBalanceEntriesHodlingYou(db *badger.DB, pkid *PKID, filterOutZeroBalances bool) ([]*BalanceEntry, error) {
	// Get the balance entries for the coins that *hold you*
	balanceEntriesThatHodlYou := []*BalanceEntry{}
	{
		prefix := append([]byte{}, _PrefixCreatorPKIDHODLerPKIDToBalanceEntry...)
		keyPrefix := append(prefix, pkid[:]...)
		_, entryByteStringsFound := _enumerateKeysForPrefix(db, keyPrefix)
		for _, byteString := range entryByteStringsFound {
			currentEntry := &BalanceEntry{}
			currentEntry.Decode(byteString)
			if filterOutZeroBalances && currentEntry.BalanceNanos == 0 {
				continue
			}
			balanceEntriesThatHodlYou = append(balanceEntriesThatHodlYou, currentEntry)
		}
	}

	return balanceEntriesThatHodlYou, nil
}

// =====================================================================================
// End coin balance entry code
// =====================================================================================

// startPrefix specifies a point in the DB at which the iteration should start.
// It doesn't have to map to an exact key because badger will just binary search
// and start right before/after that location.
//
// validForPrefix helps determine when the iteration should stop. The iteration
// stops at the last entry that has this prefix. Setting it to
// an empty byte string would cause the iteration to seek to the beginning of the db,
// whereas setting it to one of the _Prefix bytes would cause the iteration to stop
// at the last entry with that prefix.
//
// maxKeyLen is required so we can pad the key with FF in the case the user wants
// to seek backwards. This is required due to a quirk of badgerdb. It is ignored
// if reverse == false.
//
// numToFetch specifies the number of entries to fetch. If set to zero then it
// fetches all entries that match the validForPrefix passed in.
func DBGetPaginatedKeysAndValuesForPrefixWithTxn(
	txn *badger.Txn, startPrefix []byte, validForPrefix []byte,
	maxKeyLen int, numToFetch int, reverse bool, fetchValues bool) (

	_keysFound [][]byte, _valsFound [][]byte, _err error) {

	keysFound := [][]byte{}
	valsFound := [][]byte{}

	opts := badger.DefaultIteratorOptions

	opts.PrefetchValues = fetchValues

	// Optionally go in reverse order.
	opts.Reverse = reverse

	it := txn.NewIterator(opts)
	defer it.Close()
	prefix := startPrefix
	if reverse {
		// When we iterate backwards, the prefix must be bigger than all possible
		// keys that could actually exist with this prefix. We achieve this by
		// padding the end of the dbPrefixx passed in up to the key length.
		prefix = make([]byte, maxKeyLen)
		for ii := 0; ii < maxKeyLen; ii++ {
			if ii < len(startPrefix) {
				prefix[ii] = startPrefix[ii]
			} else {
				prefix[ii] = 0xFF
			}
		}
	}
	for it.Seek(prefix); it.ValidForPrefix(validForPrefix); it.Next() {
		keyCopy := it.Item().KeyCopy(nil)
		if maxKeyLen != 0 && len(keyCopy) != maxKeyLen {
			return nil, nil, fmt.Errorf(
				"DBGetPaginatedKeysAndValuesForPrefixWithTxn: Invalid key length %v != %v",
				len(keyCopy), maxKeyLen)
		}

		var valCopy []byte
		if fetchValues {
			var err error
			valCopy, err = it.Item().ValueCopy(nil)
			if err != nil {
				return nil, nil, fmt.Errorf("DBGetPaginatedKeysAndValuesForPrefixWithTxn: "+
					"Error fetching value: %v", err)
			}
		}

		keysFound = append(keysFound, keyCopy)
		valsFound = append(valsFound, valCopy)

		if numToFetch != 0 && len(keysFound) == numToFetch {
			break
		}
	}

	// Return whatever we found.
	return keysFound, valsFound, nil
}

func DBGetPaginatedKeysAndValuesForPrefix(
	db *badger.DB, startPrefix []byte, validForPrefix []byte,
	keyLen int, numToFetch int, reverse bool, fetchValues bool) (
	_keysFound [][]byte, _valsFound [][]byte, _err error) {

	keysFound := [][]byte{}
	valsFound := [][]byte{}

	dbErr := db.View(func(txn *badger.Txn) error {
		var err error
		keysFound, valsFound, err = DBGetPaginatedKeysAndValuesForPrefixWithTxn(
			txn, startPrefix, validForPrefix, keyLen,
			numToFetch, reverse, fetchValues)
		if err != nil {
			return fmt.Errorf("DBGetPaginatedKeysAndValuesForPrefix: %v", err)
		}
		return nil
	})
	if dbErr != nil {
		return nil, nil, dbErr
	}

	return keysFound, valsFound, nil
}

func DBGetPaginatedPostsOrderedByTime(
	db *badger.DB, snap *Snapshot, startPostTimestampNanos uint64,
	startPostHash *BlockHash, numToFetch int, fetchPostEntries bool, reverse bool) (
	_postHashes []*BlockHash, _tstampNanos []uint64, _postEntries []*PostEntry,
	_err error) {

	startPostPrefix := append([]byte{}, _PrefixTstampNanosPostHash...)

	if startPostTimestampNanos > 0 {
		startTstampBytes := EncodeUint64(startPostTimestampNanos)
		startPostPrefix = append(startPostPrefix, startTstampBytes...)
	}

	if startPostHash != nil {
		startPostPrefix = append(startPostPrefix, startPostHash[:]...)
	}

	// We fetch in reverse to get the latest posts.
	maxUint64Tstamp := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	postIndexKeys, _, err := DBGetPaginatedKeysAndValuesForPrefix(
		db, startPostPrefix, _PrefixTstampNanosPostHash, /*validForPrefix*/
		len(_PrefixTstampNanosPostHash)+len(maxUint64Tstamp)+HashSizeBytes, /*keyLen*/
		numToFetch, reverse /*reverse*/, false /*fetchValues*/)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("DBGetPaginatedPostsOrderedByTime: %v", err)
	}

	// Cut the post hashes and timestamps out of the returned keys.
	postHashes := []*BlockHash{}
	tstamps := []uint64{}
	startTstampIndex := len(_PrefixTstampNanosPostHash)
	hashStartIndex := len(_PrefixTstampNanosPostHash) + len(maxUint64Tstamp)
	hashEndIndex := hashStartIndex + HashSizeBytes
	for _, postKeyBytes := range postIndexKeys {
		currentPostHash := &BlockHash{}
		copy(currentPostHash[:], postKeyBytes[hashStartIndex:hashEndIndex])
		postHashes = append(postHashes, currentPostHash)

		tstamps = append(tstamps, DecodeUint64(
			postKeyBytes[startTstampIndex:hashStartIndex]))
	}

	// Fetch the PostEntries if desired.
	var postEntries []*PostEntry
	if fetchPostEntries {
		for _, postHash := range postHashes {
			postEntry := DBGetPostEntryByPostHash(db, snap, postHash)
			if postEntry == nil {
				return nil, nil, nil, fmt.Errorf("DBGetPaginatedPostsOrderedByTime: "+
					"PostHash %v does not have corresponding entry", postHash)
			}
			postEntries = append(postEntries, postEntry)
		}
	}

	return postHashes, tstamps, postEntries, nil
}

func DBGetProfilesByUsernamePrefixAndDeSoLocked(db *badger.DB,
	snap *Snapshot, usernamePrefix string, utxoView *UtxoView) (
	_profileEntries []*ProfileEntry, _err error) {

	startPrefix := append([]byte{}, _PrefixProfileUsernameToPKID...)
	lowercaseUsernamePrefixString := strings.ToLower(usernamePrefix)
	lowercaseUsernamePrefix := []byte(lowercaseUsernamePrefixString)
	startPrefix = append(startPrefix, lowercaseUsernamePrefix...)

	_, pkidsFound, err := DBGetPaginatedKeysAndValuesForPrefix(
		db /*db*/, startPrefix, /*startPrefix*/
		startPrefix /*validForPrefix*/, 0, /*keyLen (ignored when reverse == false)*/
		0 /*numToFetch (zero fetches all)*/, false, /*reverse*/
		true /*fetchValues*/)
	if err != nil {
		return nil, fmt.Errorf("DBGetProfilesByUsernamePrefixAndDeSoLocked: %v", err)
	}

	// Have to do this to convert the PKIDs back into public keys
	// TODO: We should clean things up around public keys vs PKIDs
	pubKeysMap := make(map[PkMapKey][]byte)
	for _, pkidBytes := range pkidsFound {
		if len(pkidBytes) != btcec.PubKeyBytesLenCompressed {
			continue
		}
		pkid := &PKID{}
		copy(pkid[:], pkidBytes)
		pubKey := DBGetPublicKeyForPKID(db, snap, pkid)
		if len(pubKey) != 0 {
			pubKeysMap[MakePkMapKey(pubKey)] = pubKey
		}
	}

	for username, profileEntry := range utxoView.ProfileUsernameToProfileEntry {
		if strings.HasPrefix(string(username[:]), lowercaseUsernamePrefixString) {
			pkMapKey := MakePkMapKey(profileEntry.PublicKey)
			pubKeysMap[pkMapKey] = profileEntry.PublicKey
		}
	}

	// Sigh.. convert the public keys *back* into PKIDs...
	profilesFound := []*ProfileEntry{}
	for _, pk := range pubKeysMap {
		pkid := utxoView.GetPKIDForPublicKey(pk).PKID
		profile := utxoView.GetProfileEntryForPKID(pkid)
		// Double-check that a username matches the prefix.
		// If a user had the handle "elon" and then changed to "jeff" and that transaction hadn't mined yet,
		// we would return the profile for "jeff" when we search for "elon" which is incorrect.
		if profile != nil && strings.HasPrefix(strings.ToLower(string(profile.Username[:])), lowercaseUsernamePrefixString) {
			profilesFound = append(profilesFound, profile)
		}
	}

	// If there is no error, sort and return numToFetch. Username searches are always
	// sorted by coin value.
	sort.Slice(profilesFound, func(ii, jj int) bool {
		return profilesFound[ii].CoinEntry.DeSoLockedNanos > profilesFound[jj].CoinEntry.DeSoLockedNanos
	})

	return profilesFound, nil
}

// DBGetPaginatedProfilesByDeSoLocked returns up to 'numToFetch' profiles from the db.
func DBGetPaginatedProfilesByDeSoLocked(
	db *badger.DB, snap *Snapshot, startDeSoLockedNanos uint64,
	startProfilePubKeyy []byte, numToFetch int, fetchProfileEntries bool) (
	_profilePublicKeys [][]byte, _profileEntries []*ProfileEntry, _err error) {

	// Convert the start public key to a PKID.
	pkidEntry := DBGetPKIDEntryForPublicKey(db, snap, startProfilePubKeyy)

	startProfilePrefix := append([]byte{}, _PrefixCreatorDeSoLockedNanosCreatorPKID...)
	var startDeSoLockedBytes []byte
	if pkidEntry != nil {
		startDeSoLockedBytes = EncodeUint64(startDeSoLockedNanos)
		startProfilePrefix = append(startProfilePrefix, startDeSoLockedBytes...)
		startProfilePrefix = append(startProfilePrefix, pkidEntry.PKID[:]...)
	} else {
		// If no pub key is provided, we just max out deso locked and start at the top of the list.
		maxBigEndianUint64Bytes := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
		startDeSoLockedBytes = maxBigEndianUint64Bytes
		startProfilePrefix = append(startProfilePrefix, startDeSoLockedBytes...)
	}

	keyLen := len(_PrefixCreatorDeSoLockedNanosCreatorPKID) + len(startDeSoLockedBytes) + btcec.PubKeyBytesLenCompressed
	// We fetch in reverse to get the profiles with the most DeSo locked.
	profileIndexKeys, _, err := DBGetPaginatedKeysAndValuesForPrefix(
		db, startProfilePrefix, _PrefixCreatorDeSoLockedNanosCreatorPKID, /*validForPrefix*/
		keyLen /*keyLen*/, numToFetch,
		true /*reverse*/, false /*fetchValues*/)
	if err != nil {
		return nil, nil, fmt.Errorf("DBGetPaginatedProfilesByDeSoLocked: %v", err)
	}

	// Cut the pkids out of the returned keys.
	profilePKIDs := [][]byte{}
	startPKIDIndex := len(_PrefixCreatorDeSoLockedNanosCreatorPKID) + len(startDeSoLockedBytes)
	endPKIDIndex := startPKIDIndex + btcec.PubKeyBytesLenCompressed
	for _, profileKeyBytes := range profileIndexKeys {
		currentPKID := make([]byte, btcec.PubKeyBytesLenCompressed)
		copy(currentPKID[:], profileKeyBytes[startPKIDIndex:endPKIDIndex][:])
		profilePKIDs = append(profilePKIDs, currentPKID)
	}

	profilePubKeys := [][]byte{}
	for _, pkidBytes := range profilePKIDs {
		pkid := &PKID{}
		copy(pkid[:], pkidBytes)
		profilePubKeys = append(profilePubKeys, DBGetPublicKeyForPKID(db, snap, pkid))
	}

	if !fetchProfileEntries {
		return profilePubKeys, nil, nil
	}

	// Fetch the ProfileEntries if desired.
	var profileEntries []*ProfileEntry
	for _, profilePKID := range profilePKIDs {
		pkid := &PKID{}
		copy(pkid[:], profilePKID)
		profileEntry := DBGetProfileEntryForPKID(db, snap, pkid)
		if profileEntry == nil {
			return nil, nil, fmt.Errorf("DBGetAllProfilesByLockedDeSo: "+
				"ProfilePKID %v does not have corresponding entry",
				PkToStringBoth(profilePKID))
		}
		profileEntries = append(profileEntries, profileEntry)
	}

	return profilePubKeys, profileEntries, nil
}

// -------------------------------------------------------------------------------------
// Mempool Txn mapping funcions
// <prefix, txn hash BlockHash> -> <*MsgDeSoTxn>
// -------------------------------------------------------------------------------------

func _dbKeyForMempoolTxn(mempoolTx *MempoolTx) []byte {
	// Make a copy to avoid multiple calls to this function re-using the same slice.
	prefixCopy := append([]byte{}, _PrefixMempoolTxnHashToMsgDeSoTxn...)
	timeAddedBytes := EncodeUint64(uint64(mempoolTx.Added.UnixNano()))
	key := append(prefixCopy, timeAddedBytes...)
	key = append(key, mempoolTx.Hash[:]...)

	return key
}

func DbPutMempoolTxnWithTxn(txn *badger.Txn, snap *Snapshot, mempoolTx *MempoolTx) error {

	mempoolTxnBytes, err := mempoolTx.Tx.ToBytes(false /*preSignatureBool*/)
	if err != nil {
		return errors.Wrapf(err, "DbPutMempoolTxnWithTxn: Problem encoding mempoolTxn to bytes.")
	}

	if err := DBSetWithTxn(txn, snap, _dbKeyForMempoolTxn(mempoolTx), mempoolTxnBytes); err != nil {
		return errors.Wrapf(err, "DbPutMempoolTxnWithTxn: Problem putting mapping for txn hash: %s", mempoolTx.Hash.String())
	}

	return nil
}

func DbPutMempoolTxn(handle *badger.DB, snap *Snapshot, mempoolTx *MempoolTx) error {

	return handle.Update(func(txn *badger.Txn) error {
		return DbPutMempoolTxnWithTxn(txn, snap, mempoolTx)
	})
}

func DbGetMempoolTxnWithTxn(txn *badger.Txn, snap *Snapshot, mempoolTx *MempoolTx) *MsgDeSoTxn {

	mempoolTxnObj := &MsgDeSoTxn{}
	mempoolTxnBytes, err := DBGetWithTxn(txn, snap, _dbKeyForMempoolTxn(mempoolTx))
	if err != nil {
		return nil
	}

	if err = gob.NewDecoder(bytes.NewReader(mempoolTxnBytes)).Decode(mempoolTxnObj); err != nil {
		glog.Errorf("DbGetMempoolTxnWithTxn: Problem reading "+
			"Tx for tx hash %s: %v", mempoolTx.Hash.String(), err)
		return nil
	}
	return mempoolTxnObj
}

func DbGetMempoolTxn(db *badger.DB, snap *Snapshot, mempoolTx *MempoolTx) *MsgDeSoTxn {
	var ret *MsgDeSoTxn
	db.View(func(txn *badger.Txn) error {
		ret = DbGetMempoolTxnWithTxn(txn, snap, mempoolTx)
		return nil
	})
	return ret
}

func DbGetAllMempoolTxnsSortedByTimeAdded(handle *badger.DB) (_mempoolTxns []*MsgDeSoTxn, _error error) {
	_, valuesFound := _enumerateKeysForPrefix(handle, _PrefixMempoolTxnHashToMsgDeSoTxn)

	mempoolTxns := []*MsgDeSoTxn{}
	for _, mempoolTxnBytes := range valuesFound {
		mempoolTxn := &MsgDeSoTxn{}
		err := mempoolTxn.FromBytes(mempoolTxnBytes)
		if err != nil {
			return nil, errors.Wrapf(err, "DbGetAllMempoolTxnsSortedByTimeAdded: failed to decode mempoolTxnBytes.")
		}
		mempoolTxns = append(mempoolTxns, mempoolTxn)
	}

	// We don't need to sort the transactions because the DB keys include the time added and
	// are therefore retrieved from badger in order.

	return mempoolTxns, nil
}

func DbDeleteAllMempoolTxnsWithTxn(txn *badger.Txn, snap *Snapshot) error {
	txnKeysFound, _, err := _enumerateKeysForPrefixWithTxn(txn, _PrefixMempoolTxnHashToMsgDeSoTxn)
	if err != nil {
		return errors.Wrapf(err, "DbDeleteAllMempoolTxnsWithTxn: ")
	}

	for _, txnKey := range txnKeysFound {
		err := DbDeleteMempoolTxnKeyWithTxn(txn, snap, txnKey)
		if err != nil {
			return errors.Wrapf(err, "DbDeleteAllMempoolTxMappings: Deleting mempool txnKey failed.")
		}
	}

	return nil
}

func FlushMempoolToDbWithTxn(txn *badger.Txn, snap *Snapshot, allTxns []*MempoolTx) error {
	for _, mempoolTx := range allTxns {
		err := DbPutMempoolTxnWithTxn(txn, snap, mempoolTx)
		if err != nil {
			return errors.Wrapf(err, "FlushMempoolToDb: Putting "+
				"mempool tx hash %s failed.", mempoolTx.Hash.String())
		}
	}

	return nil
}

func FlushMempoolToDb(handle *badger.DB, snap *Snapshot, allTxns []*MempoolTx) error {
	err := handle.Update(func(txn *badger.Txn) error {
		return FlushMempoolToDbWithTxn(txn, snap, allTxns)
	})
	if err != nil {
		return err
	}

	return nil
}

func DbDeleteAllMempoolTxns(handle *badger.DB, snap *Snapshot) error {
	handle.Update(func(txn *badger.Txn) error {
		return DbDeleteAllMempoolTxnsWithTxn(txn, snap)
	})

	return nil
}

func DbDeleteMempoolTxnWithTxn(txn *badger.Txn, snap *Snapshot, mempoolTx *MempoolTx) error {

	// When a mapping exists, delete it.
	if err := DBDeleteWithTxn(txn, snap, _dbKeyForMempoolTxn(mempoolTx)); err != nil {
		return errors.Wrapf(err, "DbDeleteMempoolTxMappingWithTxn: Deleting "+
			"mempool tx key failed.")
	}

	return nil
}

func DbDeleteMempoolTxn(handle *badger.DB, snap *Snapshot, mempoolTx *MempoolTx) error {
	return handle.Update(func(txn *badger.Txn) error {
		return DbDeleteMempoolTxnWithTxn(txn, snap, mempoolTx)
	})
}

func DbDeleteMempoolTxnKey(handle *badger.DB, snap *Snapshot, txnKey []byte) error {
	return handle.Update(func(txn *badger.Txn) error {
		return DbDeleteMempoolTxnKeyWithTxn(txn, snap, txnKey)
	})
}

func DbDeleteMempoolTxnKeyWithTxn(txn *badger.Txn, snap *Snapshot, txnKey []byte) error {

	// When a mapping exists, delete it.
	if err := DBDeleteWithTxn(txn, snap, txnKey); err != nil {
		return errors.Wrapf(err, "DbDeleteMempoolTxMappingWithTxn: Deleting "+
			"mempool tx key failed.")
	}

	return nil
}

func LogDBSummarySnapshot(db *badger.DB) {
	keyCountMap := make(map[byte]int)
	for prefixByte := byte(0); prefixByte < byte(40); prefixByte++ {
		keysForPrefix, _ := EnumerateKeysForPrefix(db, []byte{prefixByte})
		keyCountMap[prefixByte] = len(keysForPrefix)
	}
	glog.Info(spew.Printf("LogDBSummarySnapshot: Current DB summary snapshot: %v", keyCountMap))
}

func StartDBSummarySnapshots(db *badger.DB) {
	// Periodically count the number of keys for each prefix in the DB and log.
	// Note: every 30 seconds? That's a looot of DB scans. Should remove
	// This is interesting
	go func() {
		for {
			// Figure out how many keys there are for each prefix and log.
			glog.Info("StartDBSummarySnapshots: Counting DB keys...")
			LogDBSummarySnapshot(db)
			time.Sleep(30 * time.Second)
		}
	}()
}
