package p2psplit

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// The protocols of model splitting over P2P.
const (
	// ProtoControl is the control session that a split worker keeps open to
	// its server: the worker says what it is, the server answers and, for
	// MLX, assigns the rank. The worker dials it.
	ProtoControl = "/localai/split/1"
	// ProtoLlamaRPC carries one connection of llama.cpp RPC from the server
	// to the rpc-server of a worker. The server dials it.
	ProtoLlamaRPC = "/localai/llama-rpc/1"
	// ProtoMLX carries one connection of the MLX ring. The stream starts
	// with a header that names the rank it goes to. The server relays a
	// stream between two workers, so workers need no link to each other.
	ProtoMLX = "/localai/mlx/1"
)

// Engine is the inference engine that a split worker lends its memory to.
type Engine string

// The engines.
const (
	EngineLlama Engine = "llama"
	EngineMLX   Engine = "mlx"
)

// Valid reports whether e is a known engine.
func (e Engine) Valid() bool { return e == EngineLlama || e == EngineMLX }

// EngineLabel is the scope label of a split invite that names the engine.
const EngineLabel = "engine"

// protoVersion is the version of the control messages.
const protoVersion = 1

// maxMessage bounds one message of the control session or a ring header.
const maxMessage = 16 << 10

// heartbeatEvery is how often the worker writes a heartbeat on the control
// session; the server drops a session it heard nothing from for
// sessionTimeout.
const (
	heartbeatEvery = 15 * time.Second
	sessionTimeout = 3 * heartbeatEvery
)

// The message types of the control session. Hello, heartbeat: worker to
// server. Welcome, refused, assign, moved, wait: server to worker.
const (
	msgHello     = "hello"
	msgWelcome   = "welcome"
	msgRefused   = "refused"
	msgAssign    = "assign"
	msgHeartbeat = "heartbeat"
	// msgMoved names the peer that serves the worker now: a backend node
	// of a cluster. The worker opens its control session there.
	msgMoved = "moved"
	// msgWait ends a session without refusing the worker: the peer it
	// should use is not ready. The worker asks the server again later.
	msgWait = "wait"
)

// message is one message of the control session.
type message struct {
	Type string `json:"type"`
	V    int    `json:"v,omitempty"`
	// Hello.
	Engine      Engine `json:"engine,omitempty"`
	Name        string `json:"name,omitempty"`
	Version     string `json:"version,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	// Refused, wait.
	Err string `json:"error,omitempty"`
	// Moved: the peer ID and the multiaddrs of the backend node that
	// serves the worker.
	Peer  string   `json:"peer,omitempty"`
	Addrs []string `json:"addrs,omitempty"`
	// Assign (MLX): the rank of the worker in a group of Size ranks. Rank
	// zero is the server; a worker with Rank 0 stands by.
	Rank int `json:"rank,omitempty"`
	Size int `json:"size,omitempty"`
	// Anyway is set when the administrator chose "Use anyway" for a relay
	// of theirs that limits each connection: the ring streams may use it.
	Anyway bool `json:"anyway,omitempty"`
}

// ringHeader starts a ProtoMLX stream.
type ringHeader struct {
	// Rank is the rank the stream goes to.
	Rank int `json:"rank"`
}

// writeMsg writes v as a uvarint length and JSON.
func writeMsg(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > maxMessage {
		return errors.New("p2psplit: message too large")
	}
	var n [binary.MaxVarintLen64]byte
	l := binary.PutUvarint(n[:], uint64(len(b)))
	if _, err := w.Write(append(n[:l:l], b...)); err != nil {
		return err
	}
	return nil
}

// byteReader reads one byte at a time and buffers nothing, so that a stream
// can be handed to a pipe after its header.
type byteReader struct{ r io.Reader }

func (b byteReader) ReadByte() (byte, error) {
	var c [1]byte
	if _, err := io.ReadFull(b.r, c[:]); err != nil {
		return 0, err
	}
	return c[0], nil
}

// readMsg reads one message written by writeMsg. It reads no byte past the
// message.
func readMsg(r io.Reader, v any) error {
	n, err := binary.ReadUvarint(byteReader{r})
	if err != nil {
		return err
	}
	if n > maxMessage {
		return fmt.Errorf("p2psplit: message of %d bytes is too large", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
