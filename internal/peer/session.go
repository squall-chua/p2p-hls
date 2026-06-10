package peer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/squall-chua/p2p-hls/internal/identity"
	peerv1 "github.com/squall-chua/p2p-hls/proto/peer/v1"
)

// ProtocolVersion is the wire-protocol version exchanged at handshake.
const ProtocolVersion = 1

// Signaler ships a signed signal to a remote Node (over the signaling server).
type Signaler interface {
	SendSignal(to identity.NodeID, s SignedSignal) error
}

// Session is one identity-verified WebRTC connection to a remote Node.
type Session struct {
	self     *identity.Identity
	remote   identity.NodeID
	signaler Signaler

	pc *webrtc.PeerConnection

	ids     requestIDs
	mu      sync.Mutex
	control *webrtc.DataChannel
	pending map[uint64]chan *peerv1.Envelope

	readyOnce sync.Once
	ready     chan struct{}

	handler         RequestHandler
	onAccessGranted func(identity.NodeID)

	bulk         *webrtc.DataChannel
	bulkSinks    map[uint64]*bulkSink
	mediaHandler MediaHandler
	lowSig       chan struct{}

	partyHandler PartyHandler
	swarmHandler SwarmHandler
	remoteCaps   []string
	onClose      func(identity.NodeID)
}

// NewSession builds a Session and its underlying PeerConnection.
func NewSession(self *identity.Identity, remote identity.NodeID, cfg webrtc.Configuration, sig Signaler) (*Session, error) {
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return nil, fmt.Errorf("new peer connection: %w", err)
	}
	s := &Session{
		self:      self,
		remote:    remote,
		signaler:  sig,
		pc:        pc,
		pending:   make(map[uint64]chan *peerv1.Envelope),
		ready:     make(chan struct{}),
		bulkSinks: make(map[uint64]*bulkSink),
		lowSig:    make(chan struct{}, 1),
	}
	// Answerer receives channels created by the initiator.
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case "control":
			s.bindControl(dc)
		case "bulk":
			s.bindBulk(dc)
		}
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		switch st {
		case webrtc.PeerConnectionStateDisconnected,
			webrtc.PeerConnectionStateFailed,
			webrtc.PeerConnectionStateClosed:
			s.mu.Lock()
			fn := s.onClose
			s.mu.Unlock()
			if fn != nil {
				fn(s.remote)
			}
		}
	})
	return s, nil
}

// Start drives the offer/answer exchange. initiator=true creates channels + offer.
func (s *Session) Start(ctx context.Context, initiator bool) error {
	if !initiator {
		return nil // answerer acts on the incoming offer in HandleSignal
	}
	control, err := s.pc.CreateDataChannel("control", nil)
	if err != nil {
		return err
	}
	s.bindControl(control)
	bulk, err := s.pc.CreateDataChannel("bulk", nil)
	if err != nil {
		return err
	}
	s.bindBulk(bulk)
	offer, err := s.pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	return s.setLocalAndSend(ctx, offer)
}

// HandleSignal processes an inbound offer or answer.
func (s *Session) HandleSignal(sig SignedSignal) error {
	if sig.From != string(s.remote) {
		return fmt.Errorf("peer: signal from unexpected node %s", sig.From)
	}
	desc, err := VerifySignal(sig)
	if err != nil {
		return err
	}
	if err := s.pc.SetRemoteDescription(desc); err != nil {
		return err
	}
	if desc.Type == webrtc.SDPTypeOffer {
		answer, err := s.pc.CreateAnswer(nil)
		if err != nil {
			return err
		}
		// TODO(slice-3): thread a real context here; the answerer's ICE gather currently has no timeout.
		return s.setLocalAndSend(context.Background(), answer)
	}
	return nil
}

// setLocalAndSend sets the local description, waits for non-trickle ICE gathering,
// then signs and relays the complete SDP.
func (s *Session) setLocalAndSend(ctx context.Context, desc webrtc.SessionDescription) error {
	gatherComplete := webrtc.GatheringCompletePromise(s.pc)
	if err := s.pc.SetLocalDescription(desc); err != nil {
		return err
	}
	select {
	case <-gatherComplete:
	case <-ctx.Done():
		return ctx.Err()
	}
	signed, err := SignSignal(s.self, *s.pc.LocalDescription())
	if err != nil {
		return err
	}
	return s.signaler.SendSignal(s.remote, signed)
}

// bindControl wires the control channel: send a Handshake on open, dispatch
// inbound Envelopes (Handshake -> ready, Ping -> Pong, Pong -> pending).
func (s *Session) bindControl(dc *webrtc.DataChannel) {
	s.mu.Lock()
	s.control = dc
	s.mu.Unlock()
	dc.OnOpen(func() {
		_ = s.send(&peerv1.Envelope{
			Body: &peerv1.Envelope_Handshake{Handshake: &peerv1.Handshake{
				ProtocolVersion: ProtocolVersion,
				Capabilities:    []string{CapParty},
			}},
		})
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		env, err := decodeEnvelope(msg.Data)
		if err != nil {
			return
		}
		switch body := env.Body.(type) {
		case *peerv1.Envelope_Handshake:
			s.mu.Lock()
			s.remoteCaps = body.Handshake.GetCapabilities()
			s.mu.Unlock()
			if body.Handshake.GetProtocolVersion() == ProtocolVersion {
				s.readyOnce.Do(func() { close(s.ready) })
			}
		case *peerv1.Envelope_Ping:
			_ = s.send(&peerv1.Envelope{
				RequestId: env.RequestId,
				Body:      &peerv1.Envelope_Pong{Pong: &peerv1.Pong{Nonce: body.Ping.GetNonce()}},
			})
		case *peerv1.Envelope_Browse:
			s.handleBrowse(env.RequestId)
		case *peerv1.Envelope_GetMetadata:
			s.handleGetMetadata(env.RequestId, body.GetMetadata.GetContentId())
		case *peerv1.Envelope_RequestAccess:
			s.handleRequestAccess(env.RequestId, body.RequestAccess.GetMessage())
		case *peerv1.Envelope_AccessGranted:
			s.mu.Lock()
			fn := s.onAccessGranted
			s.mu.Unlock()
			if fn != nil {
				fn(s.remote)
			}
		case *peerv1.Envelope_GetPlaylist:
			s.handleGetPlaylist(env.RequestId, body.GetPlaylist)
		case *peerv1.Envelope_GetSegment:
			s.handleGetSegment(env.RequestId, body.GetSegment)
		case *peerv1.Envelope_Download:
			s.handleDownload(env.RequestId, body.Download)
		case *peerv1.Envelope_Error:
			s.mu.Lock()
			sink := s.bulkSinks[env.RequestId]
			s.mu.Unlock()
			if sink != nil {
				s.signalSink(sink, statusErr(body.Error))
			} else {
				s.deliver(env)
			}
		case *peerv1.Envelope_JoinParty:
			s.handleJoinParty(env.RequestId, body.JoinParty.GetContentId())
		case *peerv1.Envelope_LeaveParty:
			if h := s.currentPartyHandler(); h != nil {
				h.OnLeaveParty(s.remote, body.LeaveParty.GetPartyId())
			}
		case *peerv1.Envelope_PartyState:
			if h := s.currentPartyHandler(); h != nil {
				h.OnPartyState(s.remote, body.PartyState)
			}
		case *peerv1.Envelope_PartyAudience:
			if h := s.currentPartyHandler(); h != nil {
				h.OnPartyAudience(s.remote, body.PartyAudience)
			}
		case *peerv1.Envelope_PartyInvite:
			if h := s.currentPartyHandler(); h != nil {
				h.OnPartyInvite(s.remote, body.PartyInvite)
			}
		case *peerv1.Envelope_PartyEnded:
			if h := s.currentPartyHandler(); h != nil {
				h.OnPartyEnded(s.remote, body.PartyEnded)
			}
		case *peerv1.Envelope_PartyDanmaku:
			if h := s.currentPartyHandler(); h != nil {
				h.OnPartyDanmaku(s.remote, body.PartyDanmaku)
			}
		case *peerv1.Envelope_SwarmHave:
			if h := s.currentSwarmHandler(); h != nil {
				h.OnSwarmHave(s.remote, body.SwarmHave)
			}
		case *peerv1.Envelope_GetSwarmSegment:
			s.handleGetSwarmSegment(env.RequestId, body.GetSwarmSegment)
		case *peerv1.Envelope_Pong, *peerv1.Envelope_Catalog,
			*peerv1.Envelope_TitleMeta, *peerv1.Envelope_Ack, *peerv1.Envelope_Playlist_,
			*peerv1.Envelope_PartyWelcome:
			s.deliver(env)
		}
	})
}

func (s *Session) send(env *peerv1.Envelope) error {
	raw, err := encodeEnvelope(env)
	if err != nil {
		return err
	}
	s.mu.Lock()
	dc := s.control
	s.mu.Unlock()
	if dc == nil {
		return fmt.Errorf("peer: control channel not open")
	}
	return dc.Send(raw)
}

// Ready is closed once the version handshake succeeds.
func (s *Session) Ready() <-chan struct{} { return s.ready }

// Ping sends a ping and returns the echoed nonce.
func (s *Session) Ping(ctx context.Context, nonce string) (string, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_Ping{Ping: &peerv1.Ping{Nonce: nonce}}})
	if err != nil {
		return "", err
	}
	if e := resp.GetError(); e != nil {
		return "", statusErr(e)
	}
	return resp.GetPong().GetNonce(), nil
}

// Close tears down the connection.
// TODO(slice-3): cancel in-flight Pings (close a done channel) and tear down pending when Close gains real connection lifecycle management.
func (s *Session) Close() error { return s.pc.Close() }

// RequestHandler answers inbound RPCs. Installed per Session by the app layer.
type RequestHandler interface {
	Browse(remote identity.NodeID) ([]*peerv1.TitleMeta, error)
	GetMetadata(remote identity.NodeID, contentID string) (*peerv1.TitleMeta, error)
	RequestAccess(remote identity.NodeID, message string) error
}

// SetHandler installs the inbound-request handler.
func (s *Session) SetHandler(h RequestHandler) {
	s.mu.Lock()
	s.handler = h
	s.mu.Unlock()
}

// currentHandler returns the installed handler under lock.
func (s *Session) currentHandler() RequestHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handler
}

// OnAccessGranted registers a callback fired when the remote grants us access.
func (s *Session) OnAccessGranted(fn func(identity.NodeID)) {
	s.mu.Lock()
	s.onAccessGranted = fn
	s.mu.Unlock()
}

// call sends a request envelope and waits for the correlated response.
func (s *Session) call(ctx context.Context, env *peerv1.Envelope) (*peerv1.Envelope, error) {
	id := s.ids.next()
	env.RequestId = id
	ch := make(chan *peerv1.Envelope, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	if err := s.send(env); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *Session) deliver(env *peerv1.Envelope) {
	s.mu.Lock()
	ch, ok := s.pending[env.RequestId]
	delete(s.pending, env.RequestId)
	s.mu.Unlock()
	if ok {
		ch <- env
	}
}

// Browse fetches the remote's Catalog.
func (s *Session) Browse(ctx context.Context) ([]*peerv1.TitleMeta, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_Browse{Browse: &peerv1.Browse{}}})
	if err != nil {
		return nil, err
	}
	if e := resp.GetError(); e != nil {
		return nil, statusErr(e)
	}
	return resp.GetCatalog().GetTitles(), nil
}

// GetMetadata fetches one Title's metadata.
func (s *Session) GetMetadata(ctx context.Context, contentID string) (*peerv1.TitleMeta, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_GetMetadata{GetMetadata: &peerv1.GetMetadata{ContentId: contentID}}})
	if err != nil {
		return nil, err
	}
	if e := resp.GetError(); e != nil {
		return nil, statusErr(e)
	}
	return resp.GetTitleMeta(), nil
}

// RequestAccess asks the remote Host to allow this Node.
func (s *Session) RequestAccess(ctx context.Context, message string) error {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_RequestAccess{RequestAccess: &peerv1.RequestAccess{Message: message}}})
	if err != nil {
		return err
	}
	if e := resp.GetError(); e != nil {
		return statusErr(e)
	}
	return nil // Ack
}

// SendAccessGranted notifies the remote Viewer that access was approved.
func (s *Session) SendAccessGranted() error {
	return s.send(&peerv1.Envelope{Body: &peerv1.Envelope_AccessGranted{AccessGranted: &peerv1.AccessGranted{}}})
}

func (s *Session) handleBrowse(reqID uint64) {
	h := s.currentHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	titles, err := h.Browse(s.remote)
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_Catalog{Catalog: &peerv1.Catalog{Titles: titles}}})
}

func (s *Session) handleGetMetadata(reqID uint64, contentID string) {
	h := s.currentHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	meta, err := h.GetMetadata(s.remote, contentID)
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_TitleMeta{TitleMeta: meta}})
}

func (s *Session) handleRequestAccess(reqID uint64, message string) {
	h := s.currentHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	if err := h.RequestAccess(s.remote, message); err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_Ack{Ack: &peerv1.Ack{}}})
}

// bulkSink accumulates an inbound bulk transfer (segment buffer or download writer).
type bulkSink struct {
	w    io.Writer
	done chan error
}

// signalSink delivers a terminal result to a bulk sink without ever blocking the
// readLoop (done is cap-1; only one terminal result is expected per request).
func (s *Session) signalSink(sink *bulkSink, err error) {
	select {
	case sink.done <- err:
	default:
	}
}

// failSink unregisters the sink and signals it with an error.
func (s *Session) failSink(id uint64, sink *bulkSink, err error) {
	s.mu.Lock()
	delete(s.bulkSinks, id)
	s.mu.Unlock()
	s.signalSink(sink, err)
}

func (s *Session) bindBulk(dc *webrtc.DataChannel) {
	s.mu.Lock()
	s.bulk = dc
	s.mu.Unlock()
	dc.SetBufferedAmountLowThreshold(bulkLowWater)
	dc.OnBufferedAmountLow(func() {
		select {
		case s.lowSig <- struct{}{}:
		default:
		}
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// payload aliases msg.Data; consume it synchronously here.
		//
		// Late-frame safety: a bulk frame arriving after its sink was torn down by
		// a control-channel Error (or ctx cancel) is harmless. On success, this
		// goroutine sends the terminal signal only AFTER its final write and the
		// host emits no frames past `last`, so the caller's fetchBulk buffer read
		// happens-after all writes. On error, fetchBulk returns nil WITHOUT reading
		// the buffer, and the download path's writer is an *os.File whose Write/Close
		// are internally synchronized — so a straggler frame can never race an
		// observable read. Once cleanup deletes the sink, the lookup below returns nil.
		id, _, last, payload, ok := decodeBulkFrame(msg.Data)
		if !ok {
			return
		}
		s.mu.Lock()
		sink := s.bulkSinks[id]
		s.mu.Unlock()
		if sink == nil {
			return
		}
		if len(payload) > 0 {
			if _, werr := sink.w.Write(payload); werr != nil {
				s.failSink(id, sink, werr)
				return
			}
		}
		if last {
			s.signalSink(sink, nil)
		}
	})
}

// fetchBulkTo issues a bulk request and streams the response into w.
func (s *Session) fetchBulkTo(ctx context.Context, env *peerv1.Envelope, w io.Writer) error {
	id := s.ids.next()
	env.RequestId = id
	sink := &bulkSink{w: w, done: make(chan error, 1)}
	s.mu.Lock()
	s.bulkSinks[id] = sink
	s.mu.Unlock()
	cleanup := func() {
		s.mu.Lock()
		delete(s.bulkSinks, id)
		s.mu.Unlock()
	}
	if err := s.send(env); err != nil {
		cleanup()
		return err
	}
	select {
	case err := <-sink.done:
		cleanup()
		return err
	case <-ctx.Done():
		cleanup()
		return ctx.Err()
	}
}

// fetchBulk buffers a bulk response in memory (for segments).
func (s *Session) fetchBulk(ctx context.Context, env *peerv1.Envelope) ([]byte, error) {
	var buf bytes.Buffer
	if err := s.fetchBulkTo(ctx, env, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Session) sendBulkReader(reqID uint64, r io.Reader) error {
	buf := make([]byte, payloadMax)
	var seq uint32
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if werr := s.sendFrameFlow(reqID, seq, false, buf[:n]); werr != nil {
				return werr
			}
			seq++
		}
		if err == io.EOF {
			return s.sendFrameFlow(reqID, seq, true, nil)
		}
		if err != nil {
			return err
		}
	}
}

func (s *Session) sendBulk(reqID uint64, data []byte) error {
	return s.sendBulkReader(reqID, bytes.NewReader(data))
}

func (s *Session) sendFrameFlow(reqID uint64, seq uint32, last bool, payload []byte) error {
	s.mu.Lock()
	bulk := s.bulk
	s.mu.Unlock()
	if bulk == nil {
		return fmt.Errorf("peer: bulk channel not open")
	}
	for bulk.BufferedAmount() > bulkHighWater {
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-s.lowSig:
			timer.Stop()
		case <-timer.C:
			return fmt.Errorf("peer: bulk backpressure timeout")
		}
	}
	return bulk.Send(encodeBulkFrame(reqID, seq, last, payload))
}

// MediaHandler answers streaming RPCs. Installed per Session by the app layer.
type MediaHandler interface {
	Playlist(remote identity.NodeID, contentID, name string) (data []byte, contentType string, complete bool, err error)
	Segment(remote identity.NodeID, contentID, name string) ([]byte, error)
	OpenFile(remote identity.NodeID, contentID string) (io.ReadCloser, int64, error)
}

// SetMediaHandler installs the streaming handler.
func (s *Session) SetMediaHandler(h MediaHandler) {
	s.mu.Lock()
	s.mediaHandler = h
	s.mu.Unlock()
}

// currentMediaHandler returns the installed media handler under lock.
func (s *Session) currentMediaHandler() MediaHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mediaHandler
}

// GetPlaylist fetches a named playlist.
func (s *Session) GetPlaylist(ctx context.Context, contentID, name string) ([]byte, string, bool, error) {
	resp, err := s.call(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_GetPlaylist{GetPlaylist: &peerv1.GetPlaylist{ContentId: contentID, Name: name}}})
	if err != nil {
		return nil, "", false, err
	}
	if e := resp.GetError(); e != nil {
		return nil, "", false, statusErr(e)
	}
	p := resp.GetPlaylist_()
	return p.GetData(), p.GetContentType(), p.GetComplete(), nil
}

// GetSegment fetches a named segment over the bulk channel.
func (s *Session) GetSegment(ctx context.Context, contentID, name string) ([]byte, error) {
	return s.fetchBulk(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_GetSegment{GetSegment: &peerv1.GetSegment{ContentId: contentID, Name: name}}})
}

// DownloadTo streams the original file bytes into w.
func (s *Session) DownloadTo(ctx context.Context, contentID string, w io.Writer) error {
	return s.fetchBulkTo(ctx, &peerv1.Envelope{Body: &peerv1.Envelope_Download{Download: &peerv1.Download{ContentId: contentID}}}, w)
}

func (s *Session) handleGetPlaylist(reqID uint64, req *peerv1.GetPlaylist) {
	h := s.currentMediaHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	data, ct, complete, err := h.Playlist(s.remote, req.GetContentId(), req.GetName())
	if err != nil {
		_ = s.send(errEnvelope(reqID, err))
		return
	}
	_ = s.send(&peerv1.Envelope{RequestId: reqID, Body: &peerv1.Envelope_Playlist_{Playlist_: &peerv1.Playlist{
		Data: data, ContentType: ct, Complete: complete,
	}}})
}

func (s *Session) handleGetSegment(reqID uint64, req *peerv1.GetSegment) {
	h := s.currentMediaHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	go func() {
		data, err := h.Segment(s.remote, req.GetContentId(), req.GetName())
		if err != nil {
			_ = s.send(errEnvelope(reqID, err))
			return
		}
		if serr := s.sendBulk(reqID, data); serr != nil {
			_ = s.send(errEnvelope(reqID, serr))
		}
	}()
}

func (s *Session) handleDownload(reqID uint64, req *peerv1.Download) {
	h := s.currentMediaHandler()
	if h == nil {
		_ = s.send(errEnvelope(reqID, ErrUnavailable))
		return
	}
	go func() {
		rc, _, err := h.OpenFile(s.remote, req.GetContentId())
		if err != nil {
			_ = s.send(errEnvelope(reqID, err))
			return
		}
		defer rc.Close()
		if serr := s.sendBulkReader(reqID, rc); serr != nil {
			_ = s.send(errEnvelope(reqID, serr))
		}
	}()
}
