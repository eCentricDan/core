package lib

import (
	"crypto"
"crypto/sha512"
"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/NVIDIA/sortedmap"
	"github.com/btcsuite/btcd/btcec"
	"github.com/bwesterb/go-ristretto/edwards25519"
"github.com/cloudflare/circl/group"
	merkletree "github.com/deso-protocol/go-merkle-tree"
	"github.com/dgraph-io/badger/v3"
	"github.com/oleiade/lane"
	"github.com/stretchr/testify/require"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

type enhancedHeader struct {
	// Note this is encoded as a fixed-width uint32 rather than a
	// uvarint or a uint64.
	Version uint32

	// Hash of the previous block in the chain.
	PrevBlockHash *BlockHash

	// The merkle root of all the transactions contained within the block.
	TransactionMerkleRoot *BlockHash

	// The unix timestamp (in seconds) specifying when this block was
	// mined.
	TstampSecs uint64

	// The height of the block this header corresponds to.
	Height uint64

	// The nonce that is used by miners in order to produce valid blocks.
	//
	// Note: Before the upgrade from HeaderVersion0 to HeaderVersion1, miners would make
	// use of ExtraData in the BlockRewardMetadata to get extra nonces. However, this is
	// no longer needed since HeaderVersion1 upgraded the nonce to 64 bits from 32 bits.
	Nonce uint64

	// An extra nonce that can be used to provice *even more* entropy for miners, in the
	// event that ASICs become powerful enough to have birthday problems in the future.
	ExtraNonce uint64
}

func (msg *enhancedHeader) ToBytes(preSignature bool) ([]byte, error) {
	retBytes := []byte{}

	// Version
	{
		scratchBytes := [4]byte{}
		binary.BigEndian.PutUint32(scratchBytes[:], msg.Version)
		retBytes = append(retBytes, scratchBytes[:]...)
	}

	// PrevBlockHash
	prevBlockHash := msg.PrevBlockHash
	if prevBlockHash == nil {
		prevBlockHash = &BlockHash{}
	}
	retBytes = append(retBytes, prevBlockHash[:]...)

	// TransactionMerkleRoot
	transactionMerkleRoot := msg.TransactionMerkleRoot
	if transactionMerkleRoot == nil {
		transactionMerkleRoot = &BlockHash{}
	}
	retBytes = append(retBytes, transactionMerkleRoot[:]...)

	// TstampSecs
	{
		scratchBytes := [8]byte{}
		binary.BigEndian.PutUint64(scratchBytes[:], msg.TstampSecs)
		retBytes = append(retBytes, scratchBytes[:]...)

		// TODO: Don't allow this field to exceed 32-bits for now. This will
		// adjust once other parts of the code are fixed to handle the wider
		// type.
		if msg.TstampSecs > math.MaxUint32 {
			return nil, fmt.Errorf("EncodeHeaderVersion1: TstampSecs not yet allowed " +
				"to exceed max uint32. This will be fixed in the future")
		}
	}

	// Height
	{
		scratchBytes := [8]byte{}
		binary.BigEndian.PutUint64(scratchBytes[:], msg.Height)
		retBytes = append(retBytes, scratchBytes[:]...)

		// TODO: Don't allow this field to exceed 32-bits for now. This will
		// adjust once other parts of the code are fixed to handle the wider
		// type.
		if msg.Height > math.MaxUint32 {
			return nil, fmt.Errorf("EncodeHeaderVersion1: Height not yet allowed " +
				"to exceed max uint32. This will be fixed in the future")
		}
	}

	// Nonce
	{
		scratchBytes := [8]byte{}
		binary.BigEndian.PutUint64(scratchBytes[:], msg.Nonce)
		retBytes = append(retBytes, scratchBytes[:]...)
	}

	// ExtraNonce
	{
		scratchBytes := [8]byte{}
		binary.BigEndian.PutUint64(scratchBytes[:], msg.ExtraNonce)
		retBytes = append(retBytes, scratchBytes[:]...)
	}

	return retBytes, nil
}


func TestTypes(t *testing.T) {
	r := Prefixes
	fmt.Println(r)

	fmt.Println(Prefixes.PrefixBlockHashToBlock)
    v := reflect.ValueOf(*r)
	fmt.Println("ITERATING OVER ALL FIELDS")
    for i := 0; i < v.NumField(); i++ {
		fmt.Println("PREFIX:", v.Field(i).Interface())
    }

	//for i := 0; i < v.NumField(); i++ {
	//	if v.Field(i).Interface().(PrefixType).IsState() {
	//		fmt.Println("STATE PREFIX:", v.Field(i).Interface())
	//	}
	//}
}

func TestFromBytes(t *testing.T) {
	require := require.New(t)
	_ = require

	priv, err := btcec.NewPrivateKey(btcec.S256())
	require.NoError(err)
	expectedBlock.BlockProducerInfo.Signature, err = priv.Sign([]byte{0x01, 0x02, 0x03})
	require.NoError(err)

	bytesExpectedBlock, err := expectedBlock.ToBytes(false)
	require.NoError(err)

	testBlock := NewMessage(MsgTypeBlock).(*MsgDeSoBlock)
	err = testBlock.FromBytes(bytesExpectedBlock)
	require.NoError(err)

	require.Equal(*testBlock, *expectedBlock)

	expectedHeader := &MsgDeSoHeader{
		Version: 1,
		PrevBlockHash: &BlockHash{
			0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x10, 0x11,
			0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x20, 0x21,
			0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x30, 0x31,
			0x32, 0x33,
		},
		TransactionMerkleRoot: &BlockHash{
			0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x40, 0x41, 0x42, 0x43,
			0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x50, 0x51, 0x52, 0x53,
			0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x60, 0x61, 0x62, 0x63,
			0x64, 0x65,
		},
		TstampSecs: uint64(0x70717273),
		Height:     uint64(99999),
		Nonce:      uint64(123456),
	}

	enhancedHeader := &enhancedHeader{
		Version: expectedHeader.Version,
		PrevBlockHash: expectedHeader.PrevBlockHash,
		TransactionMerkleRoot: &BlockHash{
			0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x40, 0x41, 0x42, 0x43,
			0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x50, 0x51, 0x52, 0x53,
			0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x60, 0x61, 0x62, 0x63,
			0x64, 0x65,
		},
		TstampSecs: expectedHeader.TstampSecs,
		Height: expectedHeader.Height,
		Nonce: expectedHeader.Nonce,
    }

	expectedHeaderBytes, err := expectedHeader.ToBytes(false)
	enhancedHeaderBytes, err := enhancedHeader.ToBytes(false)
	testHeader := NewMessage(MsgTypeHeader).(*MsgDeSoHeader)
	err = testHeader.FromBytes(enhancedHeaderBytes)
	require.NoError(err)
	testHeaderBytes, err := testHeader.ToBytes(false)
	require.NoError(err)
	fmt.Println(expectedHeaderBytes)
	fmt.Println(testHeaderBytes)
}



func TestDeque(t *testing.T) {
	require := require.New(t)
	_ = require

	deque := lane.NewDeque()
	fmt.Println(deque.Capacity(), deque.Empty())
	for ii := 0; ii < 5; ii ++ {
		fmt.Println(deque.Append(ii))
	}

	lastElem := make(map[int]int)
	lastElem[1] = 5
	lastElem[3] = 2
	deque.Append(lastElem)
	fmt.Println(deque.Capacity(), deque.Empty())

	fmt.Println(deque.Shift())
	fmt.Println(deque.Last())
	lastElem[5] = 122
	fmt.Println(deque.Last())
	vv := deque.Last().(map[int]int)
	vv[17] = 444
	fmt.Println(deque.Last())
}

func TestBadgerConcurrentWrite(t *testing.T) {
	require := require.New(t)
	_ = require

	db, _ := GetTestBadgerDb()
	const keySize = 16
	const valSize = 32
	sequentialWrites := 128
	concurrentWrites := 512

	var keys [][keySize]byte
	var vals [][valSize]byte
	for ii := 0; ii < sequentialWrites + concurrentWrites; ii++ {
		var key [keySize]byte
		var val [valSize]byte
		copy(key[:], RandomBytes(keySize))
		copy(val[:], RandomBytes(valSize))
		keys = append(keys, key)
		vals = append(vals, val)
	}

	wait := sync.WaitGroup{}
	wait.Add(1)

	err := db.Update(func(txn *badger.Txn) error {
		for ii := 0; ii < sequentialWrites; ii++ {
			err := txn.Set(keys[ii][:], vals[ii][:])
			if err != nil {
				return err
			}
		}

		// This won't work because of concurrency
		//go func(txn *badger.Txn, wait *sync.WaitGroup) {
		//	for jj := sequentialWrites; jj < sequentialWrites + concurrentWrites; jj++ {
		//		_ = txn.Set(keys[jj][:], vals[jj][:])
		//	}
		//	wait.Done()
		//}(txn, &wait)

		go func(db *badger.DB, wait *sync.WaitGroup) {
			err := db.Update(func(txn *badger.Txn) error {
				for jj := sequentialWrites; jj < sequentialWrites + concurrentWrites; jj++ {
					err := txn.Set(keys[jj][:], vals[jj][:])
					if err != nil {
						fmt.Printf("Error in concurrent write: %v", err)
						return err
					}
				}
				return nil
			})
			if err != nil {
				fmt.Printf("Error, failed to write concurrently: %v", err)
			}
			fmt.Println("Finished concurrent write")
			wait.Done()
		}(db, &wait)
		return nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Println("Finished sequential write")

	wait.Wait()
	fmt.Println("Finished everything")

	err = db.View(func(txn *badger.Txn) error {
		for ii := 0; ii < sequentialWrites + concurrentWrites; ii++ {
			item, err := txn.Get(keys[ii][:])
			if err != nil {
				fmt.Printf("Error: %v, at index %v\n", err, ii)
				return err
			}
			value, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			require.Equal(reflect.DeepEqual(hex.EncodeToString(value), hex.EncodeToString(vals[ii][:])), true)
		}
		return nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Println("Finished comparison")
}

func TestBadgerEmptyWrite(t *testing.T) {
	require := require.New(t)

	db, _ := GetTestBadgerDb()
	key := []byte{1, 2, 3}
	val := []byte{}

	err := db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, val)
	})
	require.NoError(err)

	var readVal []byte
	err = db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}

		readVal, err = item.ValueCopy(nil)
		if err != nil {
			return err
		}
		return nil
	})
	require.NoError(err)

	fmt.Println(readVal, val)
	require.Equal(reflect.DeepEqual(hex.EncodeToString(val), hex.EncodeToString(readVal)), true)
}

// Part of the process of maintaining state snapshot involves writing
// to so-called Ancestral Records after a DB flush in utxo_view.
// To optimize the process, we will write to BadgerDB with ordered
// (key, value) pairs, which should theoretically be faster considering
// Badger's LSM tree design. In this test we benchmark the data structure
// that would store the (k,v) pairs.
// 1. A sorted map based on Left-leaning red-black trees.
// 2. A naive approach with a map and a sorted list of keys.
// Result:
// LLRB is about 2x slower than the map approach, but don't require
// storing 2x keys, which could be useful when utxo_view becomes large.
func TestSortedMap(t *testing.T) {
	LLRB := sortedmap.NewLLRBTree(sortedmap.CompareString, nil)

	nodup := make(map[string]bool)
	size := 1000000
	keySize := int32(32)
	valueSize := int32(256)

	var kList, vList []string
	for ii:=0; ii<size; ii++{
		key := hex.EncodeToString(RandomBytes(keySize))
		if _, ok := nodup[key]; ok {
			continue
		}
		value := hex.EncodeToString(RandomBytes(valueSize))
		kList = append(kList, key)
		vList = append(vList, value)
		nodup[key] = true
	}

	fmt.Printf("Total number of (k,v) pairs to add: %v\n", len(nodup))
	fmt.Println("--------------")

	timeLLRBAddKeys := 0.0

	for ii := 0; ii < len(kList); ii++ {
		k, v := kList[ii], vList[ii]
		timeStart := time.Now()
		ok, err := LLRB.Put(k, v)
		timeLLRBAddKeys += (time.Since(timeStart)).Seconds()
		require.NoError(t, err)
		require.Equal(t, ok, true)
	}
	fmt.Printf("Total time to add keys to LLRB %v\n", timeLLRBAddKeys)

	timeSMapAddKeys := 0.0
	SMap := make(map[string]string)
	SKList := make([]string, 0)
	for ii := 0; ii < len(kList); ii++ {
		k, v := kList[ii], vList[ii]
		timeStart := time.Now()
		SMap[k] = v
		SKList = append(SKList, k)
		timeSMapAddKeys += (time.Since(timeStart)).Seconds()
	}
	timeStart := time.Now()
	sort.Strings(SKList)
	timeSMapAddKeys += (time.Since(timeStart)).Seconds()
	fmt.Printf("Total time to add and sort keys in a map %v\n", timeSMapAddKeys)

	prevKey := hex.EncodeToString([]byte{0})
	timeLLRBGetKeys := 0.0
	timeSMapGetKeys := 0.0
	for i := 0; i < len(kList); i++ {
		timeStart = time.Now()
		kLLRB, vLLRB, ok, err := LLRB.GetByIndex(i)
		timeLLRBGetKeys += (time.Since(timeStart)).Seconds()
		require.NoError(t, err)
		require.Equal(t, ok, true)
		require.Greater(t, kLLRB.(string), prevKey)
		prevKey = kLLRB.(string)

		timeStart = time.Now()
		kSMap, vSMap := SKList[i], SMap[SKList[i]]
		timeSMapGetKeys += (time.Since(timeStart)).Seconds()
		require.Equal(t, kLLRB, kSMap)
		require.Equal(t, vLLRB, vSMap)
		//fmt.Printf("key: %v, value %v\n", k, v)
	}

	fmt.Println("--------------")
	fmt.Printf("Total time to fetch keys in LLRB %v\n", timeLLRBGetKeys)
	fmt.Printf("Total time to fetch keys in Sorted Map %v\n", timeSMapGetKeys)
	fmt.Println("--------------")
	fmt.Printf("Total time to add and fetch keys in LLRB %v\n", timeLLRBAddKeys + timeLLRBGetKeys)
	fmt.Printf("Total time to add and fetch keys in Sorted Map %v\n", timeSMapAddKeys + timeSMapGetKeys)
}

func TestStateChecksumBasicAddRemove(t *testing.T) {
	require := require.New(t)
	_ = require

	z := StateChecksum{}
	z.Initialize()
	identity := group.Ristretto255.Identity()
	bytesA := []byte("This is a test data")
	bytesB := []byte("This is another test")
	bytesC := []byte("This is yet another test")

	// Basic check #1
	// Compute checksum A + B, then remove B from the checksum and confirm it's equal to A
	var check1, check2, check3 group.Element
	check1 = group.Ristretto255.NewElement()
	check2 = group.Ristretto255.NewElement()
	check3 = group.Ristretto255.NewElement()
	z.AddBytes(bytesA)
	check1Bytes, _ := z.Checksum.MarshalBinary()
 	_ = check1.UnmarshalBinary(check1Bytes)
	z.AddBytes(bytesB)
 	check2Bytes, _ := z.Checksum.MarshalBinary()
	_ = check2.UnmarshalBinary(check2Bytes)
	z.RemoveBytes(bytesB)
	require.Equal(z.Checksum.IsEqual(check1), true)
	z.RemoveBytes(bytesA)
	require.Equal(z.Checksum.IsEqual(identity), true)

	// Basic check #2
	// Check if checksum A + B is equal to checksum B + A
	z.AddBytes(bytesB)
	z.AddBytes(bytesA)
	require.Equal(check2.IsEqual(z.Checksum), true)
	z.RemoveBytes(bytesA)
	z.RemoveBytes(bytesB)
	require.Equal(z.Checksum.IsEqual(identity), true)

	// Basic check #3
	// Check if checksum A + B + C is the same as C + A + B and B + A + C
	// Do some random removes to make sure everything is commutative.
	// A + B + C
	z.AddBytes(bytesA)
	z.AddBytes(bytesB)
	z.AddBytes(bytesC)
	check1Bytes, _ = z.Checksum.MarshalBinary()
	_ = check1.UnmarshalBinary(check1Bytes)
	// Remove C, A, B
	z.RemoveBytes(bytesC)
	z.RemoveBytes(bytesA)
	z.RemoveBytes(bytesB)
	require.Equal(z.Checksum.IsEqual(identity), true)

	// C + A + B
	z.AddBytes(bytesC)
	z.AddBytes(bytesA)
	z.AddBytes(bytesB)
	check2Bytes, _ = z.Checksum.MarshalBinary()
	_ = check2.UnmarshalBinary(check2Bytes)
	// Remove A, B, C
	z.RemoveBytes(bytesA)
	z.RemoveBytes(bytesB)
	z.RemoveBytes(bytesC)
	require.Equal(z.Checksum.IsEqual(identity), true)

	// Add B + A + C
	z.AddBytes(bytesB)
	z.AddBytes(bytesA)
	z.AddBytes(bytesC)
	check3Bytes, _ := z.Checksum.MarshalBinary()
	_ = check3.UnmarshalBinary(check3Bytes)
	require.Equal(check2.IsEqual(check1), true)
	require.Equal(check3.IsEqual(check1), true)
	z.RemoveBytes(bytesB)
	z.RemoveBytes(bytesA)
	z.RemoveBytes(bytesC)
	require.Equal(z.Checksum.IsEqual(identity), true)
}

type HashCoordinator struct {
	Elligator2Configs []*Elligator2Config
	MaxCount int32
}

func (coordinator *HashCoordinator) Map (t *testing.T, msg []byte, dst []byte) {
	config := Elligator2Map(t, msg, dst)
	coordinator.Elligator2Configs = append(coordinator.Elligator2Configs, config)
}

func (coordinator *HashCoordinator) Reduce(t *testing.T) *edwards25519.ExtendedPoint {
	var sqrt, twiddle, sgn, chk, corr, rSubOne, sNeg edwards25519.FieldElement
	var jc edwards25519.JacobiPoint
	var inCaseA, inCaseB, inCaseD, b int32

	if len(coordinator.Elligator2Configs) == 0 {
		return nil
	}

	// 2**252 - 3
	// 2**253 - 6
	// 2**254 - 12
	// 2**255 - 24
	// 2**255
	var ndCopy, ndCopySqrt edwards25519.FieldElement
	add(&feOne, &ND, &ndCopy)


	ndCopySqrt.Exp22523(&ndCopy)
	for ii := 0; ii < 3; ii++ {
		ndCopySqrt.Square(&ndCopySqrt)
		//fmt.Printf("sqrt #%v, value: (%v)\n", ii, ndCopySqrt.String())
	}
	for ii := 0; ii < 4; ii++ {
		ndCopySqrt.Mul(&ndCopySqrt, &ndCopy)
		//fmt.Printf("mult #%v, value: (%v)\n", ii, ndCopySqrt.String())
	}
	exp3andMult4()
	//fmt.Println("FINAL:", ndCopySqrt.String(), ndCopySqrt.String() == "1")
	require.Equal("1", ndCopySqrt.String())

	// den3, chk (local), tt, ND, r,
	// inCaseA (local), inCaseD (local), inCaseB (local), corr (local),
	// sqrt (local), twiddle (local), r0i (local), sgn (local), jc (local), rSubOne (local), sNeg (local)

	// configuration: den3, chk, ND,

	// case       A           B            C             D
	// ---------------------------------------------------------------
	// t          1/sqrt(a)   -i/sqrt(a)   1/sqrt(i*a)   -i/sqrt(i*a)
	// chk        1           -1           -i            i
	// corr       1           i            1             i
	// ret        1           1            0             0


	config.tt.Exp22523(config.tt)
	config.tt.Mul(config.tt, config.den3)
	chk.Square(config.tt)
	chk.Mul(&chk, config.ND)

	inCaseA = chk.IsOneI()
	inCaseD = chk.EqualsI(&feI)
	chk.Neg(&chk)
	inCaseB = chk.IsOneI()

	corr.SetOne()
	corr.ConditionalSet(&feI, inCaseB+inCaseD)
	config.tt.Mul(config.tt, &corr)
	sqrt.Set(config.tt)

	b = inCaseA + inCaseB
	/// here -----------------

	sqrt.Abs(&sqrt)

	twiddle.SetOne()
	twiddle.ConditionalSet(config.r0i, 1-b)
	sgn.SetOne()
	sgn.ConditionalSet(&feMinusOne, 1-b)
	sqrt.Mul(&sqrt, &twiddle)

	// s = N * sqrt * twiddle
	jc.S.Mul(&sqrt, config.N)

	// t = -sgn * sqrt * s * (r-1) * (d-1)^2 - 1
	jc.T.Neg(&sgn)
	jc.T.Mul(&sqrt, &jc.T)
	jc.T.Mul(&jc.S, &jc.T)
	jc.T.Mul(&feDMinusOneSquared, &jc.T)
	sub(config.r, &feOne, &rSubOne)
	jc.T.Mul(&rSubOne, &jc.T)
	sub(&jc.T, &feOne, &jc.T)

	sNeg.Neg(&jc.S)
	jc.S.ConditionalSet(&sNeg, equal30(jc.S.IsNegativeI(), b))

	var cp edwards25519.CompletedPoint
	cp.SetJacobiQuartic(&jc)
	var point edwards25519.ExtendedPoint
	point.SetZero()
	point.SetCompleted(&cp)
	return &point
}

func TestFasterHashToCurve(t *testing.T) {
	require := require.New(t)

	//p1 := group.Ristretto255.Identity()
	//p2 := group.Ristretto255.Identity()
	seedString := []byte("random byte string4")
	//bytes2 := []byte("random byte string2")
	dst := []byte("random-dst")

	testCounter := uint64(100000)
	for ii := uint64(0); ii < testCounter; ii++ {
		bytes := append(seedString, EncodeUint64(ii)...)

		//fmt.Println(point.MarshalBinary())
		//fmt.Println("fe:", fe.String())
		//_ = Elligator2Map(t, &fe)
		//jp := Elligator2Map(t, &fe)
		coordinator := HashCoordinator{}
		coordinator.Map(t, bytes, dst)
		point := coordinator.Reduce(t)

		elem := group.Ristretto255.HashToElement(bytes, dst)
		//fmt.Println(elem.MarshalBinaryCompress())

		var pointBytes [32]byte
		point.RistrettoInto(&pointBytes)
		elemBytes, err := elem.MarshalBinaryCompress()
		require.NoError(err)
		require.Equal(true, reflect.DeepEqual(pointBytes[:], elemBytes))
	}
}

var (
	// sqrt(-1)
	feI = edwards25519.FieldElement{
		1718705420411056, 234908883556509, 2233514472574048,
		2117202627021982, 765476049583133,
	}

	// parameter d of Edwards25519
	feD = edwards25519.FieldElement{
		929955233495203, 466365720129213, 1662059464998953,
		2033849074728123, 1442794654840575,
	}

	feOne = edwards25519.FieldElement{1, 0, 0, 0, 0}

	// 1 - d^2
	feOneMinusDSquared = edwards25519.FieldElement{
		1136626929484150, 1998550399581263, 496427632559748,
		118527312129759, 45110755273534,
	}

	// (d-1)^2
	feDMinusOneSquared = edwards25519.FieldElement{
		1507062230895904, 1572317787530805, 683053064812840,
		317374165784489, 1572899562415810,
	}

	feMinusOne = edwards25519.FieldElement{2251799813685228, 2251799813685247,
		2251799813685247, 2251799813685247, 2251799813685247}
)

// Returns 1 if b == c and 0 otherwise.  Assumes 0 <= b, c < 2^30.
func equal30(b, c int32) int32 {
	x := uint32(b ^ c)
	x--
	return int32(x >> 31)
}

// Sets fe to a + b without normalizing.  Returns fe.
func add(a, b, fe *edwards25519.FieldElement) {
	fe[0] = a[0] + b[0]
	fe[1] = a[1] + b[1]
	fe[2] = a[2] + b[2]
	fe[3] = a[3] + b[3]
	fe[4] = a[4] + b[4]
}

// Sets fe to a-b. Returns fe.
func sub(a, b, fe *edwards25519.FieldElement) {
	var t edwards25519.FieldElement
	t = *b

	t[1] += t[0] >> 51
	t[0] = t[0] & 0x7ffffffffffff
	t[2] += t[1] >> 51
	t[1] = t[1] & 0x7ffffffffffff
	t[3] += t[2] >> 51
	t[2] = t[2] & 0x7ffffffffffff
	t[4] += t[3] >> 51
	t[3] = t[3] & 0x7ffffffffffff
	t[0] += (t[4] >> 51) * 19
	t[4] = t[4] & 0x7ffffffffffff

	fe[0] = (a[0] + 0xfffffffffffda) - t[0]
	fe[1] = (a[1] + 0xffffffffffffe) - t[1]
	fe[2] = (a[2] + 0xffffffffffffe) - t[2]
	fe[3] = (a[3] + 0xffffffffffffe) - t[3]
	fe[4] = (a[4] + 0xffffffffffffe) - t[4]

}

func exp3andMult4(a *edwards25519.FieldElement, mult *edwards25519.FieldElement) *edwards25519.FieldElement {
	var exp3, mult4 edwards25519.FieldElement

	exp3.Square(a)
	for ii := 0; ii < 2; ii++ {
		exp3.Square(&exp3)
		//fmt.Printf("sqrt #%v, value: (%v)\n", ii, ndCopySqrt.String())
	}

	mult4.Square(mult)
	mult4.Square(&mult4)
	return exp3.Mul(&exp3, &mult4)
}

type Elligator2Config struct {
	den3 *edwards25519.FieldElement
	tt *edwards25519.FieldElement
	ND *edwards25519.FieldElement
	r *edwards25519.FieldElement
	r0i *edwards25519.FieldElement
	N *edwards25519.FieldElement
}

func Elligator2Map(t *testing.T, msg []byte, dst []byte) *Elligator2Config {
	xmd := group.NewExpanderMD(crypto.SHA512, dst)
	data := xmd.Expand(msg, 64)
	var ptBuf [32]byte
	h := sha512.Sum512(data)
	copy(ptBuf[:], h[:32])
	var r0 edwards25519.FieldElement
	r0.SetBytes(&ptBuf)

	var r, rPlusD, rPlusOne, D, N, ND edwards25519.FieldElement
	var r0i edwards25519.FieldElement

	// r := i * r0^2
	r0i.Mul(&r0, &feI)
	r.Mul(&r0, &r0i)

	// D := -((d*r)+1) * (r + d)
	add(&feD, &r, &rPlusD)
	D.Mul(&feD, &r)
	add(&D, &feOne, &D)
	D.Mul(&D, &rPlusD)
	D.Neg(&D)

	// N := -(d^2 - 1)(r + 1)
	add(&r, &feOne, &rPlusOne)
	N.Mul(&feOneMinusDSquared, &rPlusOne)

	// sqrt is the inverse square root of N*D or of i*N*D.
	// b=1 iff n1 is square.
	ND.Mul(&N, &D)

	// FROM HERE ----------------

	var den2, den3, den4, den6, tt edwards25519.FieldElement
	den2.Square(&ND)
	den3.Mul(&den2, &ND)
	den4.Square(&den2)
	den6.Mul(&den2, &den4)
	tt.Mul(&den6, &ND)

	// TODO: SPLIT THIS GUUY

	return &Elligator2Config{
		den3: &den3,
		tt: &tt,
		N: &N,
		ND: &ND,
		r: &r,
		r0i: &r0i,
	}
}

func Elligator2Reduce(t *testing.T, config *Elligator2Config) *edwards25519.ExtendedPoint {

}


func TestStateChecksumBirthdayParadox(t *testing.T) {
	require := require.New(t)
	_ = require

	z := StateChecksum{}
	z.Initialize()

	iterationNumber := 1
	testNumber := 1000

	// We will test adding / removing a bunch of data to the state checksum and verify
	// that the final checksum is identical regardless of the order of operation.
	seed := []byte("random-salt")
	hashes := [][]byte{}
	for ii := uint64(0); ii < uint64(testNumber); ii++ {
		seedTemp := append(seed, UintToBuf(ii)...)
		hash := []byte{}
		hash = append(hash, merkletree.Sha256DoubleHash(seedTemp)...)
		hashes = append(hashes, hash)
	}
	for jj := 0; jj < testNumber; jj++ {
		z.AddBytes(hashes[jj])
	}
	var val group.Element
	val = group.Ristretto255.NewElement()
	valBytes, _ := z.Checksum.MarshalBinary()
	_ = val.UnmarshalBinary(valBytes)
	for jj := 0; jj < testNumber; jj++ {
		z.RemoveBytes(hashes[jj])
	}

	// Build a list of indexes so we can reorder the hashes when we add / remove them.
	indexes := []int{}
	for ii := 0; ii < testNumber; ii++ {
		indexes = append(indexes, ii)
	}
	rand.Shuffle(len(indexes), func(i, j int) {
		indexes[i], indexes[j] = indexes[j], indexes[i]
	})

	//fmt.Println(indexes)
	repetitions := make(map[string]bool)
	// Test the adding / removing of the hashes iteration number of times.
	// Time how much time it took us to compute all the checksum operations.
	totalElappsed := 0.0
	for ii := 0; ii < iterationNumber; ii++ {
		rand.Shuffle(len(indexes), func(i, j int) {
			indexes[i], indexes[j] = indexes[j], indexes[i]
		})
		timeStart := time.Now()
		for jj := 0; jj < testNumber; jj++ {
			z.AddBytes(hashes[jj])
			checksumBytes, _ := z.Checksum.MarshalBinary()
			checksumString := string(checksumBytes)
			if _, exists := repetitions[checksumString]; exists {
				t.Fatalf("Found birthday paradox solution! (%v)", checksumBytes)
			}
			repetitions[checksumString] = true
		}
		require.Equal(z.Checksum.IsEqual(val), true)
		for jj := 0; jj < testNumber; jj++ {
			z.RemoveBytes(hashes[jj])
		}
		totalElappsed += (time.Since(timeStart)).Seconds()
	}
	fmt.Println(totalElappsed)
}