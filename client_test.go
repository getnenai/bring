package bring

import (
	"image"
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/deluan/bring/protocol"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Client", func() {
	var s *session
	var c *Client
	var t *mockTunnel

	BeforeEach(func() {
		t = &mockTunnel{}
		l := &DefaultLogger{Quiet: true}
		s = &session{
			In:       make(chan *protocol.Instruction, 100),
			st:       SessionActive,
			done:     make(chan bool),
			logger:   l,
			tunnel:   t,
			protocol: "vnc",
		}
		c = &Client{
			session: s,
			display: newDisplay(l),
			streams: newStreams(),
			logger:  l,
		}
	})

	Context("Active session", func() {
		It("exposes the session state", func() {
			s.st = SessionHandshake
			Expect(c.State()).To(Equal(SessionHandshake))
			s.st = SessionClosed
			Expect(c.State()).To(Equal(SessionClosed))
		})

		It("returns empty connection ID before handshake completes", func() {
			s.st = SessionHandshake
			Expect(c.ConnectionID()).To(Equal(""))
		})

		It("returns the connection ID assigned during handshake", func() {
			s.connID = "$unique-connection-id"
			Expect(c.ConnectionID()).To(Equal("$unique-connection-id"))
		})

		It("sends the mouse position to the remote server", func() {
			err := c.SendMouse(image.Pt(10, 20))
			Expect(err).To(BeNil())

			Expect(t.sent[0].Opcode).To(Equal("mouse"))
			Expect(t.sent[0].Args).To(Equal([]string{"10", "20", "0"}))
		})

		It("sends the position to the remote server", func() {
			err := c.SendMouse(image.Pt(10, 20), MouseLeft, MouseDown)
			Expect(err).To(BeNil())

			Expect(t.sent[0].Opcode).To(Equal("mouse"))
			Expect(t.sent[0].Args[2]).To(Equal(strconv.Itoa(1 + 16)))
		})

		It("sends a single keyscan to the remote server", func() {
			err := c.SendKey(KeyBackspace, false)
			keyBackspace := keySyms[KeyBackspace]

			Expect(err).To(BeNil())
			Expect(t.sent).To(HaveLen(len(keyBackspace)))
			Expect(t.sent[0].Opcode).To(Equal("key"))
			Expect(t.sent[0].Args).To(Equal([]string{strconv.Itoa(keyBackspace[0]), "0"}))
		})

		It("sends a key with multiple keyscans to the remote server", func() {
			err := c.SendKey(KeyRightShift, true)
			KeyRightShift := keySyms[KeyRightShift]

			Expect(err).To(BeNil())
			Expect(t.sent).To(HaveLen(len(KeyRightShift)))
			Expect(t.sent[0].Opcode).To(Equal("key"))
			Expect(t.sent[0].Args).To(Equal([]string{strconv.Itoa(KeyRightShift[0]), "1"}))
			Expect(t.sent[1].Opcode).To(Equal("key"))
			Expect(t.sent[1].Args).To(Equal([]string{strconv.Itoa(KeyRightShift[1]), "1"}))
		})

		It("returns an ErrInvalidKeyCode when receiving an invalid keycode", func() {
			err := c.SendKey(KeyCode(math.MaxInt32), true)

			Expect(err).To(Equal(ErrInvalidKeyCode))
			Expect(t.sent).To(BeEmpty())
		})

		It("sends a text as individual keystrokes", func() {
			err := c.SendText("bring")
			Expect(err).To(BeNil())
			Expect(t.sent).To(HaveLen(10))
			for i, c := range "bring" {
				Expect(t.sent[i*2]).To(Equal(protocol.NewInstruction("key", toAscii(c), "1")))
				Expect(t.sent[i*2+1]).To(Equal(protocol.NewInstruction("key", toAscii(c), "0")))
			}
		})
	})

	Context("Session is disconnected", func() {
		BeforeEach(func() {
			s.st = SessionClosed
		})

		It("does not send anything", func() {
			err := c.SendKey(KeyEnter, true)
			Expect(err).To(Equal(ErrNotConnected))

			err = c.SendText("abc")
			Expect(err).To(Equal(ErrNotConnected))

			err = c.SendMouse(image.Pt(0, 0), MouseRight)
			Expect(err).To(Equal(ErrNotConnected))
		})
	})
})

// Integration tests using fakeServer to exercise the full NewClient → Start flow.
var _ = Describe("Client (integration)", func() {
	var (
		server *fakeServer
		c      *Client
	)

	BeforeEach(func() {
		server = &fakeServer{
			replies: map[string]string{
				"select":  "4.args,8.hostname,4.port,8.password;",
				"connect": "5.ready,21.$unique-connection-id;",
			},
		}
		addr := server.start()
		var err error
		c, err = NewClient(addr, "rdp", map[string]string{
			"hostname": "host1",
			"port":     "port1",
			"password": "password123",
		}, &DefaultLogger{Quiet: true})
		Expect(err).To(BeNil())

		Eventually(func() SessionState {
			return c.State()
		}, 3*time.Second, 100*time.Millisecond).Should(Equal(SessionActive))
	})

	AfterEach(func() {
		if c != nil {
			c.Stop()
		}
		if server != nil {
			server.stop()
		}
	})

	It("returns the connection ID after a real handshake", func() {
		Expect(c.ConnectionID()).To(Equal("$unique-connection-id"))
	})

	It("has a non-empty connection ID as soon as State becomes SessionActive", func() {
		// rdp.go reads ConnectionID immediately after observing SessionActive;
		// verify the ID is already populated at that point.
		Expect(c.State()).To(Equal(SessionActive))
		Expect(c.ConnectionID()).NotTo(BeEmpty())
	})
})

func toAscii(c int32) string {
	return strconv.Itoa(int(c))
}

type mockTunnel struct {
	protocol.Tunnel
	sent []*protocol.Instruction
}

func (mt *mockTunnel) SendInstruction(ins ...*protocol.Instruction) error {
	mt.sent = append(mt.sent, ins...)
	return nil
}

var _ = Describe("OnInputDrain", func() {
	var (
		s *session
		c *Client
	)

	BeforeEach(func() {
		l := &DefaultLogger{Quiet: true}
		s = &session{
			In:       make(chan *protocol.Instruction, 16),
			st:       SessionActive,
			done:     make(chan bool),
			logger:   l,
			tunnel:   &mockTunnel{},
			protocol: "rdp",
		}
		c = &Client{
			session: s,
			display: newDisplay(l),
			streams: newStreams(),
			logger:  l,
		}
	})

	It("fires the callback once per inbound nop", func() {
		var calls int32
		c.OnInputDrain(func() { atomic.AddInt32(&calls, 1) })

		go c.Start()

		s.In <- protocol.NewInstruction("nop")
		s.In <- protocol.NewInstruction("nop")
		s.In <- protocol.NewInstruction("nop")

		Eventually(func() int32 { return atomic.LoadInt32(&calls) },
			2*time.Second, 10*time.Millisecond).Should(Equal(int32(3)))

		close(s.done)
	})

	It("panics on a second registration to surface wiring bugs", func() {
		c.OnInputDrain(func() {})
		Expect(func() {
			c.OnInputDrain(func() {})
		}).To(PanicWith(ContainSubstring("OnInputDrain already registered")))
	})

	It("panics on a nil callback to prevent silent no-op registration", func() {
		// A nil first call must not stealth-bypass the write-once guard and let
		// a second call install a real handler.
		Expect(func() {
			c.OnInputDrain(nil)
		}).To(PanicWith(ContainSubstring("must not be nil")))
	})

	It("is a no-op when no callback is registered", func() {
		// No OnInputDrain registered; an inbound nop must not panic or block.
		go c.Start()

		s.In <- protocol.NewInstruction("nop")
		s.In <- protocol.NewInstruction("nop")

		// Give the dispatch loop a moment to process; if the handler
		// panicked, the goroutine would have died and the next push would
		// hang indefinitely. We rely on Eventually's timeout-on-failure.
		Eventually(func() int { return len(s.In) },
			2*time.Second, 10*time.Millisecond).Should(Equal(0))

		close(s.done)
	})
})

var _ = Describe("Client.Stop()", func() {
	It("unblocks a goroutine running Start()", func() {
		server := &fakeServer{
			replies: map[string]string{
				"select":  "4.args,8.hostname,4.port,8.password;",
				"connect": "5.ready,21.$unique-connection-id;",
			},
		}
		addr := server.start()
		c, err := NewClient(addr, "rdp", map[string]string{
			"hostname": "host1",
			"port":     "port1",
			"password": "password123",
		}, &DefaultLogger{Quiet: true})
		Expect(err).To(BeNil())

		startDone := make(chan struct{})
		go func() {
			defer close(startDone)
			c.Start()
		}()

		Eventually(func() SessionState {
			return c.State()
		}, 3*time.Second, 100*time.Millisecond).Should(Equal(SessionActive))

		// Without Stop() / a done-aware Start(), this goroutine blocks forever.
		c.Stop()

		select {
		case <-startDone:
			// pass — goroutine exited
		case <-time.After(2 * time.Second):
			Fail("Start() goroutine did not exit after Stop() was called")
		}
	})
})
