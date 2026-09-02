package geocoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"travelmcp/internal/httpx"
)

type YandexGeocoder struct {
	key    string
	client *httpx.Client
}

func NewYandex(key string, client *httpx.Client) *YandexGeocoder {
	return &YandexGeocoder{key: key, client: client}
}

func (y *YandexGeocoder) Geocode(ctx context.Context, query string) (*Result, error) {
	if y.key == "" {
		return nil, fmt.Errorf("yandex geocode key empty")
	}
	u := fmt.Sprintf("https://geocode-maps.yandex.ru/1.x/?format=json&apikey=%s&geocode=%s", url.QueryEscape(y.key), url.QueryEscape(query))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := y.client.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geocode %d", resp.StatusCode)
	}
	var data struct {
		Response struct {
			GeoObjectCollection struct {
				FeatureMember []struct {
					GeoObject struct {
						Point struct {
							Pos string `json:"pos"`
						} `json:"Point"`
					} `json:"GeoObject"`
				} `json:"featureMember"`
			} `json:"GeoObjectCollection"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Response.GeoObjectCollection.FeatureMember) == 0 {
		return nil, fmt.Errorf("not found")
	}
	var lon, lat float64
	fmt.Sscan(data.Response.GeoObjectCollection.FeatureMember[0].GeoObject.Point.Pos, &lon, &lat)
	return &Result{Lat: lat, Lon: lon, Name: query}, nil
}

func (y *YandexGeocoder) Reverse(_ context.Context, lat, lon float64) (string, error) {
	return fmt.Sprintf("%.5f,%.5f", lat, lon), nil
}
