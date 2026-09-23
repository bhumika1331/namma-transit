package api

import (
	"time"

	"github.com/bhumika1331/namma-transit/backend/internal/fare"
	"github.com/bhumika1331/namma-transit/backend/internal/network"
)

// NetworkStatus adapts a built network and fare tables to StatusSource.
type NetworkStatus struct {
	Net   *network.Network
	Fares *fare.Tables
}

func (s NetworkStatus) Datasets() []DatasetStatus {
	out := make([]DatasetStatus, 0, len(s.Net.Datasets))
	for _, d := range s.Net.Datasets {
		out = append(out, DatasetStatus{
			Name: d.Name, Source: d.Source, LoadedAt: s.Net.LoadedAt,
			Stops: d.Stops, Routes: d.Routes, Trips: d.Trips,
		})
	}
	return out
}

func (s NetworkStatus) FaresUpdatedAt() time.Time {
	var latest time.Time
	if s.Fares == nil {
		return latest
	}
	for _, c := range []string{s.Fares.Ordinary.EffectiveFrom, s.Fares.Vajra.EffectiveFrom, s.Fares.Metro.EffectiveFrom, s.Fares.Auto.EffectiveFrom} {
		if t, err := time.Parse("2006-01-02", c); err == nil && t.After(latest) {
			latest = t
		}
	}
	return latest
}
