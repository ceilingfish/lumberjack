package daemon

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// HealthService is a second ServiceRegistrar, present to demonstrate the seam:
// it exposes the standard grpc.health.v1 protocol (what generic tooling and
// load balancers probe) alongside LumberjackService, wired identically through
// the grpc.services group. Application-level health lives on
// LumberjackService.Health; this is the transport-level check.
type HealthService struct {
	srv *health.Server
}

// NewHealthService builds a health server reporting SERVING for the whole
// daemon. Set per-service status via srv.SetServingStatus if needed later.
func NewHealthService() *HealthService {
	srv := health.NewServer()
	srv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	return &HealthService{srv: srv}
}

// RegisterGRPC satisfies ServiceRegistrar.
func (h *HealthService) RegisterGRPC(g *grpc.Server) {
	healthpb.RegisterHealthServer(g, h.srv)
}
