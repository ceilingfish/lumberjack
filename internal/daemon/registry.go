package daemon

import (
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

// ServiceRegistrar is the extensibility seam for the daemon's gRPC surface.
// Every gRPC service the daemon exposes implements it; each is contributed
// into the "grpc.services" fx value group, and newGRPCServer collects the
// whole group and registers them all against a single *grpc.Server.
//
// Adding a service is therefore local: implement ServiceRegistrar and hand its
// constructor to asRegistrar in a module's fx.Provide. No central switch to
// edit, no ordering to maintain — fx assembles the group.
type ServiceRegistrar interface {
	// RegisterGRPC binds the service onto the server. Implementations call the
	// buf-generated RegisterXServer(g, s) for their service.
	RegisterGRPC(g *grpc.Server)
}

// grpcGroup is the fx value-group tag shared by every registrar. Kept in one
// place so a provider and the collecting param can never drift apart.
const grpcGroup = `group:"grpc.services"`

// asRegistrar annotates a constructor so its result joins the grpc.services
// group as a ServiceRegistrar. Use it around any constructor whose concrete
// type satisfies ServiceRegistrar:
//
//	fx.Provide(asRegistrar(NewServer))
func asRegistrar(constructor any) any {
	return fx.Annotate(
		constructor,
		fx.As(new(ServiceRegistrar)),
		fx.ResultTags(grpcGroup),
	)
}
