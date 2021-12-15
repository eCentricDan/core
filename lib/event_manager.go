package lib

import (
	"github.com/deso-protocol/core"
	"github.com/deso-protocol/core/net"
	"github.com/deso-protocol/core/view"
)

type TransactionEventFunc func(event *TransactionEvent)
type BlockEventFunc func(event *BlockEvent)

type TransactionEvent struct {
	Txn     *net.MsgDeSoTxn
	TxnHash *core.BlockHash

	// Optional
	UtxoView *view.UtxoView
	UtxoOps  []*view.UtxoOperation
}

type BlockEvent struct {
	Block *net.MsgDeSoBlock

	// Optional
	UtxoView *view.UtxoView
	UtxoOps  [][]*view.UtxoOperation
}

type EventManager struct {
	transactionConnectedHandlers []TransactionEventFunc
	blockConnectedHandlers       []BlockEventFunc
	blockDisconnectedHandlers    []BlockEventFunc
	blockAcceptedHandlers        []BlockEventFunc
}

func NewEventManager() *EventManager {
	return &EventManager{}
}

func (em *EventManager) OnTransactionConnected(handler TransactionEventFunc) {
	em.transactionConnectedHandlers = append(em.transactionConnectedHandlers, handler)
}

func (em *EventManager) transactionConnected(event *TransactionEvent) {
	for _, handler := range em.transactionConnectedHandlers {
		handler(event)
	}
}

func (em *EventManager) OnBlockConnected(handler BlockEventFunc) {
	em.blockConnectedHandlers = append(em.blockConnectedHandlers, handler)
}

func (em *EventManager) blockConnected(event *BlockEvent) {
	for _, handler := range em.blockConnectedHandlers {
		handler(event)
	}
}

func (em *EventManager) OnBlockDisconnected(handler BlockEventFunc) {
	em.blockDisconnectedHandlers = append(em.blockDisconnectedHandlers, handler)
}

func (em *EventManager) blockDisconnected(event *BlockEvent) {
	for _, handler := range em.blockDisconnectedHandlers {
		handler(event)
	}
}

func (em *EventManager) OnBlockAccepted(handler BlockEventFunc) {
	em.blockAcceptedHandlers = append(em.blockAcceptedHandlers, handler)
}

func (em *EventManager) blockAccepted(event *BlockEvent) {
	for _, handler := range em.blockAcceptedHandlers {
		handler(event)
	}
}
