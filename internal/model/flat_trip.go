package model

type FlatStop struct {
	StopID string   `json:"stop_id"`
	Name   string   `json:"name"`
	Region string   `json:"region"`
	OpCode string   `json:"op_code,omitempty"`
	Lat    *float64 `json:"lat,omitempty"`
	Lon    *float64 `json:"lon,omitempty"`
	ArrMin *int     `json:"arr_min,omitempty"`
	DepMin *int     `json:"dep_min,omitempty"`
}

type FlatTrip struct {
	RouteReg      string     `json:"route_reg"`
	Direction     string     `json:"direction"`
	ServiceID     int64      `json:"service_id"`
	Run           int        `json:"run"`
	Period        string     `json:"period"`
	Carrier       string     `json:"carrier"`
	CarrierINN    string     `json:"carrier_inn"`
	RouteFrom     string     `json:"route_from"`
	RouteTo       string     `json:"route_to"`
	Stops         []FlatStop `json:"stops"`
	Untimed       []string   `json:"untimed,omitempty"`
	FrequencyOnly bool       `json:"frequency_only"`
	Weekdays      []int      `json:"weekdays,omitempty"`
}
