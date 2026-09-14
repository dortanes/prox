package sdk

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"github.com/vmihailenco/msgpack/v5"
)

// startSocketListener creates a Unix socket and starts accepting connections.
// Returns the socket path. Each connection handles one request at a time
// (prox maintains a connection pool for concurrency).
func startSocketListener(p *Plugin) (string, error) {
	sockPath := fmt.Sprintf("/tmp/prox-p-%d.sock", os.Getpid())

	// Clean up stale socket from a previous run.
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return "", fmt.Errorf("listen unix %s: %w", sockPath, err)
	}

	go acceptLoop(ln, p)

	return sockPath, nil
}

func acceptLoop(ln net.Listener, p *Plugin) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		go handleConn(conn, p)
	}
}

func handleConn(conn net.Conn, p *Plugin) {
	defer conn.Close()

	for {
		env, err := readFrame(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("socket read error: %v", err)
			}
			return
		}

		// Fire-and-forget: process without response.
		if env.Hook == HookDisconnect {
			handleDisconnect(p, env.Data)
			continue
		}

		var respBytes []byte

		switch env.Hook {
		case HookRequest:
			respBytes = handleRequest(p, env.Data)
		case HookResponse:
			respBytes = handleResponse(p, env.Data)
		case HookConnect:
			respBytes = handleConnect(p, env.Data)
		default:
			log.Printf("unknown hook type: %d", env.Hook)
			continue
		}

		if err := writeFrame(conn, respBytes); err != nil {
			log.Printf("socket write error: %v", err)
			return
		}
	}
}

func handleRequest(p *Plugin, data []byte) []byte {
	if p.onReq == nil {
		return mustMarshal(&Response{Allow: true})
	}

	var req Request
	if err := msgpack.Unmarshal(data, &req); err != nil {
		log.Printf("unmarshal request: %v", err)
		return mustMarshal(&Response{Allow: false, Status: 500, Body: "plugin error"})
	}

	resp := p.onReq(&req)
	if resp == nil {
		resp = &Response{Allow: true}
	}
	return mustMarshal(resp)
}

func handleResponse(p *Plugin, data []byte) []byte {
	if p.onResp == nil {
		return mustMarshal(&ResponseMod{})
	}

	// on_response receives both the request and upstream response.
	var pair responsePair
	if err := msgpack.Unmarshal(data, &pair); err != nil {
		log.Printf("unmarshal response pair: %v", err)
		return mustMarshal(&ResponseMod{})
	}

	mod := p.onResp(&pair.Req, &pair.Resp)
	if mod == nil {
		mod = &ResponseMod{}
	}
	return mustMarshal(mod)
}

func handleConnect(p *Plugin, data []byte) []byte {
	if p.onConn == nil {
		return mustMarshal(&ConnResponse{Allow: true})
	}

	var req ConnRequest
	if err := msgpack.Unmarshal(data, &req); err != nil {
		log.Printf("unmarshal connect: %v", err)
		return mustMarshal(&ConnResponse{Allow: false})
	}

	resp := p.onConn(&req)
	if resp == nil {
		resp = &ConnResponse{Allow: true}
	}
	return mustMarshal(resp)
}

func handleDisconnect(p *Plugin, data []byte) {
	if p.onDisc == nil {
		return
	}
	var event DisconnectEvent
	if err := msgpack.Unmarshal(data, &event); err != nil {
		log.Printf("unmarshal disconnect: %v", err)
		return
	}
	p.onDisc(&event)
}

// responsePair bundles request + upstream response for the on_response hook.
type responsePair struct {
	Req  Request          `msgpack:"req"`
	Resp UpstreamResponse `msgpack:"resp"`
}

// --- Frame I/O: 4-byte length prefix + msgpack payload ---

func readFrame(r io.Reader) (*Envelope, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}

	n := binary.BigEndian.Uint32(lenBuf[:])
	if n > 1<<20 { // 1 MB sanity limit
		return nil, fmt.Errorf("frame too large: %d bytes", n)
	}

	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}

	var env Envelope
	if err := msgpack.Unmarshal(buf, &env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	return &env, nil
}

func writeFrame(w io.Writer, payload []byte) error {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func mustMarshal(v interface{}) []byte {
	b, err := msgpack.Marshal(v)
	if err != nil {
		log.Printf("marshal error: %v", err)
		return []byte{}
	}
	return b
}
