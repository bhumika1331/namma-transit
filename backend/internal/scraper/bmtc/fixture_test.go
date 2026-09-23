package bmtc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// fixture is a tiny fake of the BMTC API: route "1" (UP/DOWN, 4 stops)
// and AC route "V-2" (UP only, 3 stops). Fares from stop 0 on route 1 UP
// step up at positions 2 and 3.
type fixture struct {
	t        *testing.T
	fail500  atomic.Int32 // number of 500s to return before succeeding
	requests atomic.Int32
}

func env(data any) []byte {
	b, _ := json.Marshal(map[string]any{"Issuccess": true, "Message": "Success", "RowCount": 1, "data": data, "responsecode": 200})
	return b
}

func (f *fixture) handler(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if r.Header.Get("lan") != "en" || r.Header.Get("deviceType") != "WEB" || r.Header.Get("Origin") == "" {
		http.Error(w, "missing headers", http.StatusForbidden)
		return
	}
	if f.fail500.Load() > 0 {
		f.fail500.Add(-1)
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	num := func(k string) int {
		v, _ := req[k].(float64)
		return int(v)
	}
	str := func(k string) string {
		v, _ := req[k].(string)
		return v
	}
	stop := func(id int, name string, lat, lng, km float64) map[string]any {
		return map[string]any{"stationid": id, "stationname": name, "centerlat": lat, "centerlong": lng, "distance_on_station": km, "routeid": 0, "routeno": ""}
	}
	switch strings.TrimPrefix(r.URL.Path, "/") {
	case "GetAllServiceTypes":
		w.Write(env([]map[string]any{{"servicetype": "Non AC/Ordinary", "servicetypeid": 72}, {"servicetype": "AC", "servicetypeid": 73}}))
	case "GetAllRouteList":
		w.Write(env([]map[string]any{
			{"routeid": 101, "routeno": "1 UP", "routename": "A-D", "fromstation": "A", "fromstationid": 1, "tostation": "D", "tostationid": 4},
			{"routeid": 102, "routeno": "1 DOWN", "routename": "D-A", "fromstation": "D", "fromstationid": 4, "tostation": "A", "tostationid": 1},
			{"routeid": 201, "routeno": "V-2 UP", "routename": "A-C", "fromstation": "A", "fromstationid": 1, "tostation": "C", "tostationid": 3},
			{"routeid": 999, "routeno": "BROKEN UP", "routename": "x", "fromstation": "x", "fromstationid": 9, "tostation": "y", "tostationid": 8},
		}))
	case "SearchRoute_v2":
		switch str("routetext") {
		case "1":
			w.Write(env([]map[string]any{{"routeno": "1", "routeparentid": 10}, {"routeno": "1-A", "routeparentid": 11}}))
		case "V-2":
			w.Write(env([]map[string]any{{"routeno": "V-2", "routeparentid": 20}}))
		default:
			w.Write(env([]map[string]any{}))
		}
	case "SearchByRouteDetails_v4":
		var resp map[string]any
		switch num("routeid") {
		case 10:
			resp = map[string]any{
				"up":   map[string]any{"data": []map[string]any{stop(1, "A", 12.90, 77.50, 0), stop(2, "B", 12.91, 77.50, 1.2), stop(3, "C", 12.92, 77.50, 2.5), stop(4, "D", 12.93, 77.50, 4.1)}},
				"down": map[string]any{"data": []map[string]any{stop(4, "D", 12.93, 77.50, 0), stop(3, "C", 12.92, 77.50, 1.6), stop(2, "B", 12.91, 77.50, 2.9), stop(1, "A", 12.90, 77.50, 4.1)}},
			}
		case 20:
			// No distance_on_station: loader must chain haversine.
			resp = map[string]any{
				"up":   map[string]any{"data": []map[string]any{stop(1, "A", 12.90, 77.50, 0), stop(5, "E", 12.90, 77.52, 0), stop(3, "C", 12.92, 77.50, 0)}},
				"down": map[string]any{"data": []map[string]any{}},
			}
		default:
			resp = map[string]any{"up": map[string]any{"data": []any{}}, "down": map[string]any{"data": []any{}}}
		}
		b, _ := json.Marshal(resp)
		w.Write(b)
	case "GetTimetableByRouteid_v3":
		if num("routeid") == 201 {
			w.Write([]byte(`{"Issuccess":false,"Message":"No data","data":null}`))
			return
		}
		w.Write(env([]map[string]any{{"fromstationid": "1", "tostationid": "4", "distance": "4.10",
			"tripdetails": []map[string]string{{"starttime": "08:00", "endtime": "08:20"}, {"starttime": "07:30", "endtime": "07:50"}, {"starttime": "23:50", "endtime": "00:10"}}}}))
	case "GetFareRoutes":
		from, to := num("fromStationId"), num("toStationId")
		if from != 1 {
			w.Write(env([]map[string]any{}))
			return
		}
		w.Write(env([]map[string]any{
			{"routeid": 101, "routeno": "1", "route_direction": "Up", "source_code": "A1", "destination_code": "X" + itoa(to)},
			{"routeid": 201, "routeno": "V-2", "route_direction": "Up", "source_code": "A1", "destination_code": "X" + itoa(to)},
		}))
	case "GetMobileFareData_v2":
		dest := str("destination_code")
		fare := map[string]string{"X2": "6", "X3": "12", "X4": "18", "X5": "6"}[dest]
		if num("routeid") == 201 {
			fare = map[string]string{"X5": "15", "X3": "20"}[dest]
		}
		w.Write(env([]map[string]any{{"fare": fare, "servicetype": "Non AC/Ordinary"}, {"fare": fare, "servicetype": "Electric"}}))
	default:
		http.NotFound(w, r)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func newFixture(t *testing.T) (*fixture, *httptest.Server) {
	f := &fixture{t: t}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return f, srv
}
