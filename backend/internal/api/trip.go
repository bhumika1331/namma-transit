package api

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	transitv1 "github.com/bhumika1331/namma-transit/backend/gen/transit/v1"
	"github.com/bhumika1331/namma-transit/backend/internal/domain"
	"github.com/bhumika1331/namma-transit/backend/internal/places"
	"github.com/bhumika1331/namma-transit/backend/internal/planner"
)

// TripServer implements transit.v1.TripService.
type TripServer struct {
	Planner *planner.Planner
}

func (s *TripServer) PlanTrip(ctx context.Context, req *connect.Request[transitv1.PlanTripRequest]) (*connect.Response[transitv1.PlanTripResponse], error) {
	if req.Msg.GetOrigin() == nil || req.Msg.GetDestination() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("origin and destination are required"))
	}
	resp, err := s.Planner.Plan(ctx, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(resp), nil
}

// PlaceServer implements transit.v1.PlaceService.
type PlaceServer struct {
	Index *places.Index
}

func (s *PlaceServer) Suggest(ctx context.Context, req *connect.Request[transitv1.SuggestRequest]) (*connect.Response[transitv1.SuggestResponse], error) {
	var bias *domain.LatLng
	if b := req.Msg.GetBias(); b != nil && (b.Lat != 0 || b.Lng != 0) {
		bias = &domain.LatLng{Lat: b.Lat, Lng: b.Lng}
	}
	found, fromGeo, err := s.Index.Suggest(ctx, req.Msg.GetQuery(), bias, int(req.Msg.GetLimit()))
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&transitv1.SuggestResponse{Places: found, FromGeocoder: fromGeo}), nil
}
