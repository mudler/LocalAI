package p2prelaymgr

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// protoEcho is the protocol of the throwaway peer of a circuit test.
const protoEcho protocol.ID = "/localai/relay-check/1"

// circuitTestTTL is how long the pass of the throwaway peer lives.
const circuitTestTTL = 2 * time.Minute

// Check implements Manager.
func (m *manager) Check(ctx context.Context, id string) (Check, error) {
	e, err := m.find(id)
	if err != nil {
		return Check{}, err
	}
	var c Check
	step := func(format string, a ...any) { c.Detail = append(c.Detail, fmt.Sprintf(format, a...)) }

	start := time.Now()
	if err := m.connect(ctx, e); err != nil {
		if ctx.Err() != nil {
			return c, ctx.Err()
		}
		step("connect: failed: %s", shorten(err))
		m.set(e, func(r *Relay) { r.State, r.Health = StateUnreachable, "cannot connect: "+shorten(err) })
		c.Relay = e.snapshot()
		return c, nil
	}
	c.Connected = true
	step("connect: ok in %d ms", time.Since(start).Milliseconds())

	rctx, cancel := context.WithTimeout(ctx, connectTimeout)
	res, err := client.Reserve(rctx, m.lh(), e.info)
	cancel()
	if err != nil {
		step("reserve: refused: %s", shorten(err))
	} else {
		c.Reserved = true
		step("reserve: ok, until %s", res.Expiration.UTC().Format(time.RFC3339))
	}

	relay, err := m.refresh(ctx, e, refreshOpts{readOnly: true})
	if err != nil {
		return c, err
	}
	c.Relay = relay
	c.AdminOK = relay.State == StateInSync || relay.State == StateOutOfSync
	c.InSync = relay.State == StateInSync
	step("admin: %s (%s)", relay.State, relay.Health)

	switch {
	case c.AdminOK || relay.State == StateOpen:
		if err := m.circuitTest(ctx, e, relay.State == StateOpen); err != nil {
			if ctx.Err() != nil {
				return c, ctx.Err()
			}
			step("circuit: failed: %s", shorten(err))
		} else {
			c.CircuitOK = true
			step("circuit: ok")
		}
	default:
		step("circuit: skipped, this server cannot add a test peer to the relay")
	}
	if c.AdminOK {
		if c.InSync {
			step("digest: equal")
		} else {
			step("digest: differs, use Sync to push the paired devices")
		}
	}
	return c, nil
}

// circuitTest opens a circuit through the relay to a throwaway peer and
// echoes a few bytes. The peer gets in with a one-time relay pass of this
// server (an open relay needs none), so the test goes through the same gate
// as a new device.
func (m *manager) circuitTest(ctx context.Context, e *entry, open bool) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	th, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		return fmt.Errorf("start the test peer: %w", err)
	}
	defer func() { _ = th.Close() }()
	th.SetStreamHandler(protoEcho, func(s network.Stream) {
		defer func() { _ = s.Close() }()
		if _, err := io.Copy(s, s); err != nil {
			_ = s.Reset()
		}
	})
	if err := th.Connect(ctx, e.info); err != nil {
		return fmt.Errorf("the test peer cannot connect: %w", err)
	}
	if !open {
		var b [12]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		code := hex.EncodeToString(b[:])
		if err := m.RegisterPass(ctx, e.relay.ID, code, circuitTestTTL); err != nil {
			return fmt.Errorf("register the test pass: %w", err)
		}
		defer func() {
			cctx, ccancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer ccancel()
			_ = m.CancelPass(cctx, e.relay.ID, code)
		}()
		if _, err := relayproto.UsePass(ctx, th, e.info.ID, code); err != nil {
			return fmt.Errorf("the test peer was not admitted: %w", err)
		}
	}
	if _, err := client.Reserve(ctx, th, e.info); err != nil {
		return fmt.Errorf("the test peer cannot reserve: %w", err)
	}
	conn, err := m.cfg.Host.Dial(ctx, th.ID(), protoEcho, p2pnet.WithRelays(e.info))
	if err != nil {
		return fmt.Errorf("dial through the relay: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	msg := make([]byte, 16)
	if _, err := rand.Read(msg); err != nil {
		return err
	}
	if _, err := conn.Write(msg); err != nil {
		return err
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	if !bytes.Equal(got, msg) {
		return errors.New("the echo differs")
	}
	return nil
}
