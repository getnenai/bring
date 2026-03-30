package bring

import (
	"net"
	"sync"

	"github.com/deluan/bring/protocol"
)

const disconnectOpcode = "disconnect"

type fakeServer struct {
	mu               sync.Mutex
	ln               net.Listener
	replies          map[string]string
	messagesReceived []string
	opcodesReceived  []string
}

func (s *fakeServer) start() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	s.ln = ln
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed by stop()
			}
			s.handleRequest(conn)
		}
	}()
	return ln.Addr().String()
}

// stop closes the listener, causing the accept loop to exit.
func (s *fakeServer) stop() {
	if s.ln != nil {
		s.ln.Close()
	}
}

func (s *fakeServer) handleRequest(conn net.Conn) {
	defer conn.Close()
	io := protocol.NewInstructionIO(conn)
	for {
		recv, err := io.Read()
		if err != nil {
			return
		}
		opcode := recv.Opcode

		_, err = io.WriteRaw([]byte(s.replies[opcode]))
		if err != nil {
			panic(err)
		}
		if opcode == disconnectOpcode {
			break
		}
		s.mu.Lock()
		s.messagesReceived = append(s.messagesReceived, recv.String())
		s.opcodesReceived = append(s.opcodesReceived, opcode)
		s.mu.Unlock()
	}
	_, err := io.Write(protocol.NewInstruction(disconnectOpcode))
	if err != nil {
		panic(err)
	}
}
