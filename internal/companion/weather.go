package companion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// WeatherClient fetches coarse city weather from Open-Meteo and caches it for
// half an hour.
type WeatherClient struct {
	mu        sync.Mutex
	city      string
	snapshot  WeatherSnapshot
	expiresAt time.Time
	client    *http.Client
}

// NewWeatherClient builds a weather client.
func NewWeatherClient() *WeatherClient {
	return &WeatherClient{client: &http.Client{Timeout: 8 * time.Second}}
}

// Get returns cached weather or refreshes it for the city.
func (w *WeatherClient) Get(ctx context.Context, city string) (*WeatherSnapshot, error) {
	city = strings.TrimSpace(city)
	if city == "" {
		return nil, nil
	}
	now := time.Now()
	w.mu.Lock()
	if w.city == city && now.Before(w.expiresAt) {
		snapshot := w.snapshot
		w.mu.Unlock()
		return &snapshot, nil
	}
	w.mu.Unlock()

	snapshot, err := w.fetch(ctx, city)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	w.city = city
	w.snapshot = snapshot
	w.expiresAt = now.Add(30 * time.Minute)
	w.mu.Unlock()
	return &snapshot, nil
}

func (w *WeatherClient) Cached(city string) *WeatherSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.city != strings.TrimSpace(city) || len(w.snapshot.City) == 0 {
		return nil
	}
	snapshot := w.snapshot
	return &snapshot
}

func (w *WeatherClient) fetch(ctx context.Context, city string) (WeatherSnapshot, error) {
	geocodeURL := "https://geocoding-api.open-meteo.com/v1/search?count=1&language=zh&format=json&name=" + url.QueryEscape(city)
	var geo struct {
		Results []struct {
			Name      string  `json:"name"`
			Country   string  `json:"country"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := w.getJSON(ctx, geocodeURL, &geo); err != nil {
		return WeatherSnapshot{}, err
	}
	if len(geo.Results) == 0 {
		return WeatherSnapshot{}, fmt.Errorf("未找到城市 %q", city)
	}
	place := geo.Results[0]
	forecastURL := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,weather_code,is_day",
		place.Latitude, place.Longitude,
	)
	var forecast struct {
		Current struct {
			Temperature float64 `json:"temperature_2m"`
			WeatherCode int     `json:"weather_code"`
			IsDay       int     `json:"is_day"`
		} `json:"current"`
	}
	if err := w.getJSON(ctx, forecastURL, &forecast); err != nil {
		return WeatherSnapshot{}, err
	}
	name := place.Name
	if place.Country != "" {
		name += "，" + place.Country
	}
	return WeatherSnapshot{
		City:        name,
		Description: weatherDescription(forecast.Current.WeatherCode),
		Temperature: int(forecast.Current.Temperature + 0.5),
		IsDay:       forecast.Current.IsDay == 1,
		UpdatedAt:   time.Now().UnixMilli(),
	}, nil
}

func (w *WeatherClient) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "HitK/0.1")
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("天气服务返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func weatherDescription(code int) string {
	switch code {
	case 0:
		return "晴朗"
	case 1, 2:
		return "少云"
	case 3:
		return "多云"
	case 45, 48:
		return "有雾"
	case 51, 53, 55, 56, 57:
		return "毛毛雨"
	case 61, 63, 65, 66, 67:
		return "下雨"
	case 71, 73, 75, 77:
		return "下雪"
	case 80, 81, 82:
		return "有阵雨"
	case 85, 86:
		return "有阵雪"
	case 95, 96, 99:
		return "有雷雨"
	default:
		return "天气不明"
	}
}
