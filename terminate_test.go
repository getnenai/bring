package bring

import (
	"errors"
	"sync"
	"time"

	"github.com/deluan/bring/protocol"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// noDisconnectTunnel is a controllable mock tunnel whose Disconnect() is a
// no-op. This lets us queue a "ready" instruction after Terminate() has set
// st=SessionClosed, exercising the path where startReader processes "ready"
// after the session has been terminated.
type noDisconnectTunnel struct {
	mu   sync.Mutex
	ch   chan *protocol.Instruction
	sent []*protocol.Instruction
}

func (t *noDisconnectTunnel) Connect(_ string) error { return nil }

// Disconnect intentionally does not close t.ch so startReader keeps reading
// even after Terminate() has been called.
func (t *noDisconnectTunnel) Disconnect() {}

func (t *noDisconnectTunnel) SendInstruction(ins ...*protocol.Instruction) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent = append(t.sent, ins...)
	return nil
}

func (t *noDisconnectTunnel) ReceiveInstruction() (*protocol.Instruction, error) {
	ins, ok := <-t.ch
	if !ok {
		return nil, errors.New("channel closed")
	}
	return ins, nil
}

var _ = Describe("Session Terminate guard", func() {
	It("does not transition to SessionActive if Terminate is called before ready", func() {
		ch := make(chan *protocol.Instruction, 4)
		defer close(ch) // unblocks startReader so its goroutine exits when the test ends

		// Queue "args" to trigger the handshake in startReader.
		ch <- protocol.NewInstruction("args", "hostname", "port", "password")

		mt := &noDisconnectTunnel{ch: ch}
		s := &session{
			In:       make(chan *protocol.Instruction, 100),
			st:       SessionHandshake,
			done:     make(chan bool),
			logger:   &DefaultLogger{Quiet: true},
			tunnel:   mt,
			protocol: "rdp",
			config: map[string]string{
				"hostname": "host1",
				"port":     "port1",
				"password": "password123",
			},
		}
		s.startReader()

		// Wait until startReader has sent the handshake ("connect" is last).
		Eventually(func() bool {
			mt.mu.Lock()
			defer mt.mu.Unlock()
			for _, ins := range mt.sent {
				if ins.Opcode == "connect" {
					return true
				}
			}
			return false
		}, 3*time.Second, 10*time.Millisecond).Should(BeTrue())

		// Terminate while startReader is blocked on ReceiveInstruction.
		// Disconnect() is a no-op on this tunnel so startReader keeps reading.
		s.Terminate()
		Expect(s.state()).To(Equal(SessionClosed))

		// Queue "ready". Without the guard, startReader overwrites
		// SessionClosed → SessionActive.
		ch <- protocol.NewInstruction("ready", "$test-connection-id")

		// State must remain SessionClosed.
		Consistently(func() SessionState {
			return s.state()
		}, "200ms", "10ms").Should(Equal(SessionClosed))
	})
})
