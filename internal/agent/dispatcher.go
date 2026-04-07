package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

// Dispatcher routes inbound IPC messages and owns outbound message sending.
// Task 14 expands this with real routing.
type Dispatcher struct {
	conn *ipc.Conn
	mu   sync.Mutex
}

// NewDispatcher constructs a Dispatcher bound to conn.
func NewDispatcher(conn *ipc.Conn) *Dispatcher {
	return &Dispatcher{conn: conn}
}

// Handle processes an inbound message. Task 14 adds real routing.
func (d *Dispatcher) Handle(msg ipc.OutboxMessage) {
	// no-op placeholder
	_ = msg
}

// SendControl serializes and enqueues a control-plane message.
func (d *Dispatcher) SendControl(t ipc.MsgType, payload any, ackRequired bool) error {
	var data []byte
	var err error
	if payload != nil {
		data, err = ipc.EncodePayload(payload)
		if err != nil {
			return err
		}
	}
	h := ipc.Header{
		MsgType: t,
		SeqNo:   d.conn.Seqs().Next(),
		Length:  uint32(len(data)),
	}
	if ackRequired {
		h.Flags |= ipc.FlagAckRequired
	}
	d.conn.Send(ipc.OutboxMessage{Header: h, Payload: data})
	return nil
}

func ctxWithTimeoutSeconds(n int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(n)*time.Second)
}
