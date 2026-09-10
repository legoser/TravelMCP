package main

// JSON-схема датасета реестра (scripts/extract-minstran.py).

type reestrDataset struct {
	Source    string          `json:"source"`
	Snapshot  string          `json:"snapshot"`
	Routes    []reestrRoute   `json:"routes"`
	Stops     []reestrStop    `json:"stops"`
	Carriers  []reestrCarrier `json:"carriers"`
	Schedules []reestrSched   `json:"schedules"`
	Services  []struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	} `json:"services"`
	ServiceDays []struct {
		ServiceID int64 `json:"service_id"`
		Weekday   int   `json:"weekday"`
	} `json:"service_days"`
	ServiceExceptions []struct {
		ServiceID     int64  `json:"service_id"`
		Date          string `json:"date"`
		ExceptionType string `json:"exception_type"`
	} `json:"service_exceptions"`
}

type reestrCarrier struct {
	Name    string `json:"name"`
	INN     string `json:"inn"`
	OGRN    string `json:"ogrn"`
	Address string `json:"address"`
	Email   string `json:"email"`
}

type reestrRoute struct {
	Reg            string `json:"reg"`
	Name           string `json:"name"`
	Order          any    `json:"order"`
	Carrier        string `json:"carrier"`
	CarrierINN     string `json:"carrier_inn"`
	CarrierOGRN    string `json:"carrier_ogrn"`
	CarrierAddress string `json:"carrier_address"`
	CarrierEmail   string `json:"carrier_email"`
}

type reestrStop struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Region string   `json:"region"`
	OpReg  string   `json:"op_reg"`
	Lat    *float64 `json:"lat"`
	Lon    *float64 `json:"lon"`
}

type reestrSched struct {
	Route     string            `json:"route"`
	Direction string            `json:"direction"`
	ServiceID int64             `json:"service_id"`
	Stops     []reestrSchedStop `json:"stops"`
}

type reestrSchedStop struct {
	Stop   string       `json:"stop"`
	Region string       `json:"region"`
	Winter *reestrBlock `json:"winter"`
	Summer *reestrBlock `json:"summer"`
}

type reestrBlock struct {
	Days   string   `json:"days"`
	Dep    []string `json:"dep"`
	Dwell  []string `json:"dwell"`
	Arr    []string `json:"arr"`
	Period any      `json:"period"`
}
