package motis

type Mode string

const (
	ModeWalk              Mode = "WALK"
	ModeBike              Mode = "BIKE"
	ModeCar               Mode = "CAR"
	ModeTransit           Mode = "TRANSIT"
	ModeBus               Mode = "BUS"
	ModeCoach             Mode = "COACH"
	ModeTram              Mode = "TRAM"
	ModeSubway            Mode = "SUBWAY"
	ModeRail              Mode = "RAIL"
	ModeFerry             Mode = "FERRY"
	ModeAirplane          Mode = "AIRPLANE"
	ModeSuburban          Mode = "SUBURBAN"
	ModeAerialLift        Mode = "AERIAL_LIFT"
	ModeFunicular         Mode = "FUNICULAR"
	ModeRegionalRail      Mode = "REGIONAL_RAIL"
	ModeLongDistance      Mode = "LONG_DISTANCE"
	ModeHighSpeedRail     Mode = "HIGHSPEED_RAIL"
	ModeNightRail         Mode = "NIGHT_RAIL"
	ModeOther             Mode = "OTHER"
	ModeODM               Mode = "ODM"
	ModeRideSharing       Mode = "RIDE_SHARING"
	ModeFlex              Mode = "FLEX"
)

type LocationType string

const (
	LocationTypeAddress LocationType = "ADDRESS"
	LocationTypePlace   LocationType = "PLACE"
	LocationTypeStop    LocationType = "STOP"
)

type Area struct {
	Name       string  `json:"name"`
	AdminLevel float64 `json:"adminLevel"`
	Matched    bool    `json:"matched"`
	Unique     *bool   `json:"unique,omitempty"`
	Default    *bool   `json:"default,omitempty"`
}

func (a Area) LevelInt() int { return int(a.AdminLevel) }

type Match struct {
	Type        LocationType `json:"type"`
	Category    *string      `json:"category,omitempty"`
	Tokens      [][]float64  `json:"tokens"`
	Name        string       `json:"name"`
	ID          string       `json:"id"`
	Lat         float64      `json:"lat"`
	Lon         float64      `json:"lon"`
	Level       *float64     `json:"level,omitempty"`
	Street      *string      `json:"street,omitempty"`
	HouseNumber *string      `json:"houseNumber,omitempty"`
	Country     *string      `json:"country,omitempty"`
	Zip         *string      `json:"zip,omitempty"`
	Tz          *string      `json:"tz,omitempty"`
	Areas       []Area       `json:"areas"`
	Score       float64      `json:"score"`
	Modes       []Mode       `json:"modes,omitempty"`
	Importance  *float64     `json:"importance,omitempty"`
}

type VertexType string

const (
	VertexTypeNormal    VertexType = "NORMAL"
	VertexTypeBikeShare VertexType = "BIKESHARE"
	VertexTypeTransit   VertexType = "TRANSIT"
)

type Place struct {
	Name        string     `json:"name"`
	StopID      *string    `json:"stopId,omitempty"`
	ParentID    *string    `json:"parentId,omitempty"`
	Importance  *float64   `json:"importance,omitempty"`
	Lat         float64    `json:"lat"`
	Lon         float64    `json:"lon"`
	Level       *float64   `json:"level,omitempty"`
	Tz          *string    `json:"tz,omitempty"`
	Arrival     *string    `json:"arrival,omitempty"`
	Departure   *string    `json:"departure,omitempty"`
	VertexType  *VertexType `json:"vertexType,omitempty"`
	StopCode    *string    `json:"stopCode,omitempty"`
	Description *string    `json:"description,omitempty"`
	Modes       []Mode     `json:"modes,omitempty"`
}

type ServerConfig struct {
	MotisVersion               string `json:"motisVersion"`
	HasElevation               bool   `json:"hasElevation"`
	HasRoutedTransfers         bool   `json:"hasRoutedTransfers"`
	HasStreetRouting           bool   `json:"hasStreetRouting"`
	MaxOneToManySize           float64 `json:"maxOneToManySize"`
	MaxOneToAllTravelTimeLimit float64 `json:"maxOneToAllTravelTimeLimit"`
	MaxPrePostTransitTimeLimit float64 `json:"maxPrePostTransitTimeLimit"`
	MaxDirectTimeLimit         float64 `json:"maxDirectTimeLimit"`
	ShapesDebugEnabled         bool   `json:"shapesDebugEnabled"`
}

type InitialResponse struct {
	Lat          float64      `json:"lat"`
	Lon          float64      `json:"lon"`
	Zoom         float64      `json:"zoom"`
	ServerConfig ServerConfig `json:"serverConfig"`
}

type HealthResponse struct {
	Rt   *bool `json:"rt,omitempty"`
	Gbfs *bool `json:"gbfs,omitempty"`
}

type PlanRequest struct {
	FromPlace    string  `json:"fromPlace"`
	ToPlace      string  `json:"toPlace"`
	Time         *string `json:"time,omitempty"`
	ArriveBy     *bool   `json:"arriveBy,omitempty"`
	MaxTransfers *int    `json:"maxTransfers,omitempty"`
	TransitModes []Mode  `json:"transitModes,omitempty"`
	TimetableView *bool  `json:"timetableView,omitempty"`
	Language     []string `json:"language,omitempty"`
	Radius       *float64 `json:"radius,omitempty"`
}

type Itinerary struct {
	Duration  int     `json:"duration"`
	StartTime string  `json:"startTime"`
	EndTime   string  `json:"endTime"`
	Transfers int     `json:"transfers"`
	ID        string  `json:"id"`
	Legs      []Leg   `json:"legs"`
}

type Leg struct {
	Mode      Mode   `json:"mode"`
	From      Place  `json:"from"`
	To        Place  `json:"to"`
	Duration  int    `json:"duration"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	RealTime  bool   `json:"realTime"`
	LegGeometry *EncodedPolyline `json:"legGeometry,omitempty"`
	RouteShortName *string `json:"routeShortName,omitempty"`
	TripID    *string `json:"tripId,omitempty"`
}

type EncodedPolyline struct {
	Points    string `json:"points"`
	Precision int    `json:"precision"`
	Length    int    `json:"length"`
}

type PlanResponse struct {
	RequestParameters map[string]string `json:"requestParameters"`
	DebugOutput       map[string]int    `json:"debugOutput"`
	From              Place             `json:"from"`
	To                Place             `json:"to"`
	Direct            []Itinerary       `json:"direct"`
	Itineraries       []Itinerary       `json:"itineraries"`
	PreviousPageCursor string           `json:"previousPageCursor"`
	NextPageCursor     string           `json:"nextPageCursor"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
