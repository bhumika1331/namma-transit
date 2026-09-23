package bmtc

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Flexible number: the API mixes numbers and numeric strings.
type num float64

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*n = num(f)
	return nil
}

// ServiceType from GetAllServiceTypes.
type ServiceType struct {
	Name string `json:"servicetype"`
	ID   int    `json:"servicetypeid"`
}

// RouteListItem from GetAllRouteList: one per direction.
type RouteListItem struct {
	RouteID       int    `json:"routeid"`
	RouteNo       string `json:"routeno"`
	RouteName     string `json:"routename"`
	FromStation   string `json:"fromstation"`
	FromStationID int    `json:"fromstationid"`
	ToStation     string `json:"tostation"`
	ToStationID   int    `json:"tostationid"`
}

// Base returns the route number without its " UP"/" DOWN" suffix.
func (r RouteListItem) Base() string {
	u := strings.TrimSpace(r.RouteNo)
	for _, suf := range []string{" UP", " DOWN", " Up", " Down"} {
		if strings.HasSuffix(u, suf) {
			return strings.TrimSpace(strings.TrimSuffix(u, suf))
		}
	}
	return u
}

// Direction is "UP" or "DOWN" as encoded in the route number suffix.
func (r RouteListItem) Direction() string {
	u := strings.ToUpper(strings.TrimSpace(r.RouteNo))
	if strings.HasSuffix(u, " DOWN") {
		return "DOWN"
	}
	return "UP"
}

// SearchRouteItem from SearchRoute_v2.
type SearchRouteItem struct {
	RouteNo       string `json:"routeno"`
	RouteParentID int    `json:"routeparentid"`
}

// RouteDetails is the whole SearchByRouteDetails_v4 body (not enveloped
// the same way: "up" and "down" at top level).
type RouteDetails struct {
	Up   RouteDirection `json:"up"`
	Down RouteDirection `json:"down"`
}

type RouteDirection struct {
	Data []RouteStop `json:"data"`
}

// RouteStop is one stop on a direction, with any live vehicles.
type RouteStop struct {
	StationID   int             `json:"stationid"`
	StationName string          `json:"stationname"`
	Lat         num             `json:"centerlat"`
	Lng         num             `json:"centerlong"`
	RouteID     int             `json:"routeid"`
	RouteNo     string          `json:"routeno"`
	DistanceKm  num             `json:"distance_on_station"`
	Vehicles    json.RawMessage `json:"vehicleDetails"`
}

// RoutePoint from RoutePoints.
type RoutePoint struct {
	Lat num `json:"latitude"`
	Lng num `json:"longitude"`
}

// TimetableItem from GetTimetableByRouteid_v3.
type TimetableItem struct {
	FromStationID string      `json:"fromstationid"`
	ToStationID   string      `json:"tostationid"`
	Distance      string      `json:"distance"`
	Trips         []TripTimes `json:"tripdetails"`
}

type TripTimes struct {
	Start string `json:"starttime"` // "HH:MM"
	End   string `json:"endtime"`
}

// FareRoute from GetFareRoutes: stop codes needed by GetMobileFareData_v2.
type FareRoute struct {
	RouteID         int    `json:"routeid"`
	RouteNo         string `json:"routeno"`
	Direction       string `json:"route_direction"`
	SourceCode      string `json:"source_code"`
	DestinationCode string `json:"destination_code"`
	FromStationID   int    `json:"fromstationid"`
	ToStationID     int    `json:"tostationid"`
	ToDistanceKm    num    `json:"todistance"`
}

// FareItem from GetMobileFareData_v2.
type FareItem struct {
	Fare        num    `json:"fare"`
	ServiceType string `json:"servicetype"`
}
