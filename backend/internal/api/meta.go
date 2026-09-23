package api

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	transitv1 "github.com/bhumika1331/namma-transit/backend/gen/transit/v1"
)

// DatasetStatus is what a loaded network reports about itself for Health.
type DatasetStatus struct {
	Name     string
	Source   string
	LoadedAt time.Time
	Stops    int
	Routes   int
	Trips    int
}

// StatusSource lets the meta handler ask the running system what it has loaded
// without depending on the network or store packages.
type StatusSource interface {
	Datasets() []DatasetStatus
	FaresUpdatedAt() time.Time
}

// MetaServer implements transit.v1.MetaService.
type MetaServer struct {
	version string
	status  StatusSource
}

func NewMetaServer(version string, status StatusSource) *MetaServer {
	return &MetaServer{version: version, status: status}
}

func (s *MetaServer) Health(
	_ context.Context,
	_ *connect.Request[transitv1.HealthRequest],
) (*connect.Response[transitv1.HealthResponse], error) {
	resp := &transitv1.HealthResponse{Version: s.version}
	if s.status != nil {
		for _, d := range s.status.Datasets() {
			resp.Datasets = append(resp.Datasets, &transitv1.DatasetInfo{
				Name:     d.Name,
				Source:   d.Source,
				LoadedAt: timestamppb.New(d.LoadedAt),
				Stops:    int32(d.Stops),
				Routes:   int32(d.Routes),
				Trips:    int32(d.Trips),
			})
		}
		if t := s.status.FaresUpdatedAt(); !t.IsZero() {
			resp.FaresUpdatedAt = timestamppb.New(t)
		}
	}
	return connect.NewResponse(resp), nil
}
