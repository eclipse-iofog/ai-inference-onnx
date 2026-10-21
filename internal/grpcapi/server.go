// Package grpcapi serves inference over HTTP/2.
// Without a certificate the server accepts cleartext. With a certificate it requires TLS.
// Live and ready stay open. Every other call is checked before it runs.
// Metadata, model ready, the repository index, and infer read the model directories.
// Loading and unloading a model is not supported.
package grpcapi

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/eclipse-iofog/ai-inference-onnx/api/openinference/v2"
	"github.com/eclipse-iofog/ai-inference-onnx/internal/auth"
)

const (
	statusUnauthenticated = "unauthenticated"
	statusForbidden       = "forbidden"
)

// ListenAddr returns the listen address for port.
// The host is empty, so the listener accepts every interface.
func ListenAddr(port int) string {
	return net.JoinHostPort("", strconv.Itoa(port))
}

// Server answers live and ready checks.
// It also serves server metadata, model metadata, model ready, the repository index, and infer.
// Load and unload report that this server does not load or unload models.
type Server struct {
	openinference.UnimplementedGRPCInferenceServiceServer

	checker   *auth.Checker
	ready     func() bool
	catalog   Catalog
	runner    Runner
	payload   int
	logger    *slog.Logger
	tlsConfig *tls.Config

	once sync.Once
	rpc  *grpc.Server
}

// New returns a server that uses checker for protected calls.
// ready reports whether any model can serve. A nil ready func means not ready.
// The server speaks HTTP/2 without TLS.
func New(checker *auth.Checker, ready func() bool) *Server {
	return newServer(checker, ready, nil)
}

// NewTLS returns a server that presents the certificate in tlsConfig.
// When tlsConfig requires a client certificate, a caller must present one that chains to its authorities.
// It serves the same calls as New.
func NewTLS(checker *auth.Checker, ready func() bool, tlsConfig *tls.Config) *Server {
	return newServer(checker, ready, tlsConfig)
}

func newServer(checker *auth.Checker, ready func() bool, tlsConfig *tls.Config) *Server {
	return &Server{checker: checker, ready: ready, tlsConfig: tlsConfig}
}

// Serve accepts connections on lis until Stop or GracefulStop.
func (s *Server) Serve(lis net.Listener) error {
	return s.server().Serve(lis)
}

// GracefulStop stops new calls and waits for active calls to finish.
func (s *Server) GracefulStop() {
	s.server().GracefulStop()
}

// Stop stops the server at once.
func (s *Server) Stop() {
	s.server().Stop()
}

// server builds the gRPC server on the first call.
// RegisterInfer must run before this so the receive and send limits match the payload ceiling.
func (s *Server) server() *grpc.Server {
	s.once.Do(func() {
		opts := []grpc.ServerOption{grpc.UnaryInterceptor(s.intercept)}
		if s.tlsConfig != nil {
			opts = append([]grpc.ServerOption{grpc.Creds(credentials.NewTLS(s.tlsConfig.Clone()))}, opts...)
		}
		if s.payload > 0 {
			opts = append(opts, grpc.MaxRecvMsgSize(s.payload), grpc.MaxSendMsgSize(s.payload))
		}
		s.rpc = grpc.NewServer(opts...)
		openinference.RegisterGRPCInferenceServiceServer(s.rpc, s)
	})
	return s.rpc
}

// ServerLive reports that this process can answer.
func (s *Server) ServerLive(context.Context, *openinference.ServerLiveRequest) (*openinference.ServerLiveResponse, error) {
	return &openinference.ServerLiveResponse{Live: true}, nil
}

// ServerReady reports whether any model can serve.
// A false result is still a successful response.
func (s *Server) ServerReady(context.Context, *openinference.ServerReadyRequest) (*openinference.ServerReadyResponse, error) {
	return &openinference.ServerReadyResponse{Ready: s.readyNow()}, nil
}

func (s *Server) readyNow() bool {
	if s.ready == nil {
		return false
	}
	return s.ready()
}

func (s *Server) intercept(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	fullMethod := ""
	if info != nil {
		fullMethod = info.FullMethod
	}
	model, version := requestModel(req)
	decision, err := s.checker.AuthorizeGRPC(fullMethod, model, version, authorizationHeader(ctx), time.Now())
	if err != nil || decision != auth.Allow {
		if decision == auth.Forbidden {
			return nil, status.Error(codes.PermissionDenied, statusForbidden)
		}
		return nil, status.Error(codes.Unauthenticated, statusUnauthenticated)
	}
	return handler(ctx, req)
}

func requestModel(req any) (string, string) {
	switch msg := req.(type) {
	case *openinference.ModelInferRequest:
		return msg.GetModelName(), msg.GetModelVersion()
	case *openinference.ModelMetadataRequest:
		return msg.GetName(), msg.GetVersion()
	case *openinference.ModelReadyRequest:
		return msg.GetName(), msg.GetVersion()
	default:
		return "", ""
	}
}

func authorizationHeader(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("authorization")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
