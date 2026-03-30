package bring

import (
	"sync"
	"time"

	"github.com/deluan/bring/protocol"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Session", func() {
	var (
		server *fakeServer
		s      *session
	)
	BeforeEach(func() {
		server = &fakeServer{
			replies: map[string]string{
				"select":  "4.args,8.hostname,4.port,8.password;",
				"connect": "5.ready,21.$unique-connection-id;",
			},
		}
		addr := server.start()
		s, _ = newSession(addr, "rdp", map[string]string{
			"hostname": "host1",
			"port":     "port1",
			"password": "password123",
		}, &DefaultLogger{Quiet: true})

		Eventually(func() SessionState {
			return s.state()
		}, 3*time.Second, 100*time.Millisecond).Should(Equal(SessionActive))
	})

	It("should handshake with server", func() {
		err := s.Send(protocol.NewInstruction(disconnectOpcode))
		Expect(err).To(BeNil())

		server.mu.Lock()
		defer server.mu.Unlock()
		Expect(server.opcodesReceived).To(Equal([]string{"select", "size", "audio", "video", "image", "connect"}))
		Expect(server.messagesReceived[0]).To(Equal("6.select,3.rdp;"))
		Expect(server.messagesReceived[len(server.messagesReceived)-1]).To(Equal("7.connect,5.host1,5.port1,11.password123;"))
		Expect(s.connectionID()).To(Equal("$unique-connection-id"))
	})

	It("allows concurrent reads of state and connectionID during a state transition", func() {
		// Launch 50 readers and one writer (Terminate) behind a common start gate
		// so they all begin simultaneously. Without the mutex the race detector
		// catches the write in Terminate() racing the reads in state() /
		// connectionID().
		var wg sync.WaitGroup
		start := make(chan struct{})

		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_ = s.state()
				_ = s.connectionID()
			}()
		}

		// Terminate changes st from SessionActive → SessionClosed while the
		// readers are running.
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s.Terminate()
		}()

		close(start) // release all goroutines at once
		wg.Wait()
	})
})
