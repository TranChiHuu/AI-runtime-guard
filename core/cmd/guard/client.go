package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/airuntimeguard/core/gen/runtime/v1"
	"github.com/airuntimeguard/core/server"
)

// dial connects to the daemon over its Unix domain socket.
//
// Credentials are insecure by design: the transport is a filesystem socket with
// owner-only permissions, so the OS is the access control. Adding TLS here
// would be ceremony over a channel nothing else can reach.
func dial() (pb.RuntimeClient, func(), error) {
	sock := server.SocketPath()

	conn, err := grpc.NewClient("unix://"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}

	// Fail fast with a useful message rather than hanging: a developer running
	// `guard status` with no daemon should be told to run `guard up`.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := pb.NewRuntimeClient(conn).Health(ctx, &pb.HealthRequest{}); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("no daemon at %s — run `guard up`", sock)
	}

	return pb.NewRuntimeClient(conn), func() { conn.Close() }, nil
}

func withTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
