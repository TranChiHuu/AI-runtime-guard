package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/airuntimeguard/core/brain"
	"github.com/airuntimeguard/core/domain"
	pb "github.com/airuntimeguard/core/gen/runtime/v1"
)

// Version is the daemon build identity, surfaced by Health so `guard doctor`
// can tell an adapter it is talking to something it understands.
const Version = "0.1.0"

// SupportedAPIVersions covers the current and previous major version, so a
// stale adapter degrades gracefully instead of failing closed on an upgrade.
var SupportedAPIVersions = []string{"runtime.v1"}

type Service struct {
	pb.UnimplementedRuntimeServer

	brain *brain.Brain
	now   func() time.Time

	// watchers receive session updates. Reads are cheap and writes are rare, so
	// a plain mutex is enough; a slow watcher must never stall the decision
	// path, so sends are non-blocking.
	mu       sync.Mutex
	watchers map[int]chan *pb.SessionUpdate
	nextID   int
}

func NewService(b *brain.Brain) *Service {
	return &Service{
		brain:    b,
		now:      time.Now,
		watchers: map[int]chan *pb.SessionUpdate{},
	}
}

// Decide handles a PRE-phase signal.
//
// The RPC itself does no work beyond translation: everything that matters
// happens in the Brain, which is what keeps this file free of judgment.
func (s *Service) Decide(ctx context.Context, req *pb.DecideRequest) (*pb.Decision, error) {
	sig := toDomainSignal(req.GetSignal())
	if sig.SessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "signal has no session id")
	}

	d, err := s.brain.Decide(sig)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	s.broadcast(d)
	return fromDomainDecision(d), nil
}

// Observe ingests POST-phase signals. Fire-and-forget: a failed observation
// must never surface as an error the agent waits on.
func (s *Service) Observe(stream grpc.ClientStreamingServer[pb.Signal, pb.ObserveAck]) error {
	var ack pb.ObserveAck

	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&ack)
		}
		if err != nil {
			return err
		}

		if err := s.brain.Observe(toDomainSignal(msg)); err != nil {
			ack.Dropped++
			continue
		}
		ack.Accepted++
	}
}

func (s *Service) Resolve(ctx context.Context, req *pb.ResolveRequest) (*pb.ResolveAck, error) {
	if req.GetPromptId() == "" {
		return nil, status.Error(codes.InvalidArgument, "resolve requires a prompt id")
	}

	action, err := s.brain.ResolvePrompt(toDomainResolution(req, s.now()))
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	return &pb.ResolveAck{Applied: pb.Action(action)}, nil
}

func (s *Service) GetSession(ctx context.Context, req *pb.SessionRequest) (*pb.Session, error) {
	sess, ok := s.brain.Session(req.GetSessionId())
	if !ok {
		return nil, status.Error(codes.NotFound, "no such session")
	}
	return fromDomainSession(sess), nil
}

// WatchSession streams updates. An empty session id watches everything, which
// is what `guard status --watch` and the dashboard both use.
func (s *Service) WatchSession(req *pb.SessionRequest, stream grpc.ServerStreamingServer[pb.SessionUpdate]) error {
	ch, unsubscribe := s.subscribe()
	defer unsubscribe()

	// Send current state immediately so a watcher that attaches mid-session
	// sees the world rather than waiting for the next decision.
	for _, sess := range s.brain.Sessions() {
		if req.GetSessionId() != "" && sess.ID != req.GetSessionId() {
			continue
		}
		if err := stream.Send(&pb.SessionUpdate{Session: fromDomainSession(sess)}); err != nil {
			return err
		}
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case update := <-ch:
			if req.GetSessionId() != "" && update.GetSession().GetId() != req.GetSessionId() {
				continue
			}
			if err := stream.Send(update); err != nil {
				return err
			}
		}
	}
}

func (s *Service) Health(ctx context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{
		Serving:              true,
		Version:              Version,
		ConfigVersion:        s.brain.ConfigVersion(),
		LiveSessions:         uint32(len(s.brain.Sessions())),
		SupportedApiVersions: SupportedAPIVersions,
	}, nil
}

func (s *Service) subscribe() (<-chan *pb.SessionUpdate, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextID
	s.nextID++
	ch := make(chan *pb.SessionUpdate, 16)
	s.watchers[id] = ch

	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.watchers, id)
		close(ch)
	}
}

// broadcast notifies watchers of a decision.
//
// Sends are non-blocking and drop on a full buffer: visualization must never
// delay enforcement (docs/CONSTITUTION.md, Runtime Before Analytics). A
// dashboard that misses a frame is a cosmetic problem; a decision path stalled
// behind a slow reader is a safety one.
func (s *Service) broadcast(d domain.Decision) {
	sess, ok := s.brain.Session(d.SessionID)
	if !ok {
		return
	}
	update := &pb.SessionUpdate{
		Session:        fromDomainSession(sess),
		LatestDecision: fromDomainDecision(d),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.watchers {
		select {
		case ch <- update:
		default:
		}
	}
}

// --- listener --------------------------------------------------------------

// Listen opens the Unix domain socket the daemon serves on.
//
// A UDS rather than TCP: local-first means no listening port — nothing to
// firewall, nothing reachable from the network, and filesystem permissions
// become the access control (docs/ARCHITECTURE.md §3).
func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("server: runtime dir: %w", err)
	}

	// A socket left behind by a killed daemon would block every restart. Only
	// remove it if nothing is actually listening — otherwise we would silently
	// steal a healthy daemon's socket.
	if conn, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
		conn.Close()
		return nil, fmt.Errorf("server: a daemon is already listening on %s", path)
	}
	_ = os.Remove(path)

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("server: listen: %w", err)
	}

	// Owner-only: the socket is the entire access control surface.
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("server: chmod socket: %w", err)
	}

	return ln, nil
}

// SocketPath returns the daemon socket location, honouring XDG on Linux and
// falling back to the user's home elsewhere.
func SocketPath() string {
	if p := os.Getenv("GUARD_SOCKET"); p != "" {
		return p
	}
	return filepath.Join(RuntimeDir(), "guard.sock")
}

// RuntimeDir is where all local state lives. Owner-only: it is the developer's
// data (Article IX).
func RuntimeDir() string {
	if p := os.Getenv("GUARD_HOME"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "ai-runtime-guard")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".guard"
	}
	return filepath.Join(home, ".local", "state", "ai-runtime-guard")
}

// Serve runs the gRPC server until the listener closes.
func Serve(ln net.Listener, svc *Service) error {
	g := grpc.NewServer()
	pb.RegisterRuntimeServer(g, svc)
	return g.Serve(ln)
}
